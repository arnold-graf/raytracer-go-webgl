package webgpu

// One packing path, two backends.
//
// The Metal backend runs the same megakernel over the same scene, and the one
// thing that must never drift between the two is what the buffers contain. So
// it does not pack anything itself: Packer produces the exact bytes each
// binding expects, with no GPU device involved, and each backend uploads them
// its own way -- wgpu through queue writes, Metal into MTLBuffers.
//
// This is the in-memory form of what RT_DUMP_BUFFERS writes to disk (see
// bufdump.go). The dump stays useful as a parity harness; this is what a live
// renderer uses.

import (
	"fmt"
	"regexp"

	"raytracer/internal/camera"
	"raytracer/internal/gpuscene"
	"raytracer/internal/render"
	"raytracer/internal/texture"
	"raytracer/internal/webgpu/shaders"
)

// Packer holds the scene cache between frames, so a static scene is packed once
// and a moving one re-packs only what moved.
type Packer struct {
	cache  sceneCache
	maxDim int

	captureW, captureH int
	captureVer         uint64
	captureLoaded      bool
	capturePixels      []uint32
	documentVer        uint64
	documentLoaded     bool
	documentPixels     []uint32
}

// Frame is one frame's worth of packed scene: the bytes for every binding, plus
// the acceleration-structure inputs and the dirty flags that tell a Metal
// backend whether it must rebuild its structures or may refit them.
type Frame struct {
	Bindings map[uint32][]byte
	Sizes    map[uint32]uint64

	Width, Height int
	AdaptiveAA    bool
	SoftShadows   bool
	ReflFilter    bool
	ReflHalf      bool

	// Acceleration structure inputs, in the packed BVH's own terms.
	Nodes               []GPUBVHNode
	Templates           []GPUTemplateRecord
	Instances           []GPUInstanceRecord
	BlockerSectionStart uint32

	// StaticChanged means the geometry itself moved and structures must be
	// rebuilt. TransformsChanged alone means only placements or primitive
	// spans moved, which a refit covers.
	StaticChanged     bool
	TransformsChanged bool
}

// NewPacker returns a packer sized for a maxDim x maxDim working set, matching
// what the wgpu backend allocates.
func NewPacker(maxDim int) *Packer { return &Packer{maxDim: maxDim} }

// BindingSizes is the allocation each binding needs, mirroring the bind group
// in device.go. A backend must allocate at least this much even where the
// frame's bytes are shorter, because the shader indexes the whole range.
func BindingSizes(maxDim int) map[uint32]uint64 {
	md := uint64(maxDim)
	return map[uint32]uint64{
		0:  paramsSize,
		1:  md * md * 4,
		2:  maxPrims * primStride,
		3:  maxLights * lightStride,
		4:  maxPrims * primStride,
		5:  maxBVHNodes * 4 * nodeStride,
		6:  maxTerrains * terrainStride,
		7:  maxTerrainVals * 16,
		8:  maxWaters * waterStride,
		9:  permCount * 4,
		10: uint64(gpuscene.AOVolumeFloats) * 4,
		11: maxCampfires * campfireStride,
		12: maxHoles * holeStride,
		13: uint64(maxCapturePixels(maxDim) * 4),
		14: maxInstTemplates * instTemplateStride,
		15: maxInstances * instanceStride,
		16: profileCounterBytes,
		17: idxTablesWords * 4,
		18: md * md * shadowAuxStride,
		19: uint64(texture.DocumentCount * texture.DocumentTexW * texture.DocumentTexH * 4),
		20: maxPrims * boxFacesPerPrim * 4,
		21: maxTerrainFeatures * terrainFeatureStride,
		22: maxTerrainPads * terrainPadStride,
		23: maxTerrainMipVals * 8,
		24: md * md * hdrPixStride,
		25: md * md * aaHitStride,
		26: maxTerrainZones * terrainZoneStride,
		27: maxTerrainZoneVerts * terrainZoneVertStride,
		28: md * md * 4,
		29: aaDispatchBytes,
	}
}

// Pack produces this frame's bindings. Scratch bindings -- output, shadow aux,
// hdr pixels, AA fingerprints and the task list -- carry no bytes here; the
// backend allocates them zeroed and the shader fills them.
func (p *Packer) Pack(cam *camera.Camera, v *render.View, w, h int) *Frame {
	rp := packParams(&p.cache, v)
	p.syncTextures()

	c := &p.cache
	f := &Frame{
		Bindings:            map[uint32][]byte{},
		Sizes:               BindingSizes(p.maxDim),
		Width:               w,
		Height:              h,
		AdaptiveAA:          rp.adaptiveAA,
		SoftShadows:         rp.softShadows,
		ReflFilter:          rp.reflFilter,
		ReflHalf:            rp.reflHalf,
		Nodes:               c.bvhNodes,
		Templates:           c.instTemplates,
		Instances:           c.instPlacements,
		BlockerSectionStart: blockerTreeRoot(c),
		StaticChanged:       rp.uploadStatic,
		TransformsChanged:   rp.uploadPartial,
	}

	pb := paramsBytesFor(cam, rp, w, h, p.texState())
	f.Bindings[0] = append([]byte(nil), pb[:]...)

	set := func(i uint32, b []byte) {
		if len(b) > 0 {
			f.Bindings[i] = b
		}
	}
	set(2, primBytes(rp.prims))
	set(3, lightBytes(rp.lights))
	set(4, primBytes(rp.blockers))
	set(5, nodeBytes(rp.bvhNodes))
	set(6, terrainBytes(rp.terrains))
	set(7, floatBytes(rp.samples))
	set(8, waterBytes(rp.waters))
	set(9, u32Bytes(PackPerm()))
	if rp.aoOK {
		set(10, floatBytes(rp.ao.Data))
	}
	set(11, campfireBytes(rp.campfireParams))
	set(12, holeBytes(rp.holes))
	set(13, u32Bytes(p.capturePixels))
	set(14, instTemplateBytes(rp.instTemplates))
	if n := rp.instPlacements; len(n) > 0 {
		if len(n) > maxInstances {
			n = n[:maxInstances]
		}
		set(15, instanceBytes(n))
	}
	set(19, u32Bytes(p.documentPixels))
	set(20, u32Bytes(rp.boxFaceTex))
	set(21, terrainFeatureBytes(rp.terrainFeatures))
	set(22, terrainPadBytes(rp.terrainPads))
	set(23, floatBytes(rp.terrainMips))
	set(26, terrainZoneBytes(rp.terrainZones))
	set(27, terrainZoneVertBytes(rp.terrainZoneVerts))

	// Binding 17 is three tables in one buffer, at fixed word offsets.
	idx := make([]byte, idxTablesWords*4)
	copyAt := func(wordBase int, src []byte) {
		if o := wordBase * 4; o+len(src) <= len(idx) {
			copy(idx[o:], src)
		}
	}
	copyAt(0, u32Bytes(rp.planeIdx))
	copyAt(idxTablesBlockerPlaneBase, u32Bytes(rp.blockerPlaneIdx))
	copyAt(idxTablesLightGridBase, u32Bytes(rp.lightGrid.flat()))
	f.Bindings[17] = idx

	return f
}

// syncTextures mirrors the capture/document refresh buildRenderParams does,
// minus the upload: the bytes land in the frame like any other binding.
func (p *Packer) syncTextures() {
	if ver := texture.CaptureGPUVersion(); ver != p.captureVer || !p.captureLoaded {
		p.captureLoaded = false
		p.capturePixels = nil
		if w, h, px, ok := texture.PackCapturesGPU(); ok && len(px) <= maxCapturePixels(p.maxDim) {
			p.captureW, p.captureH, p.capturePixels = w, h, px
			p.captureLoaded = true
		} else {
			p.captureW, p.captureH = 0, 0
		}
		p.captureVer = ver
	}
	if ver := texture.DocumentGPUVersion(); ver != p.documentVer || !p.documentLoaded {
		if px, ok := texture.PackDocumentsGPU(); ok {
			p.documentPixels = px
			p.documentLoaded = true
		}
		p.documentVer = ver
	}
}

func (p *Packer) texState() texState {
	return texState{
		captureW: p.captureW, captureH: p.captureH,
		captureLoaded: p.captureLoaded, documentLoaded: p.documentLoaded,
	}
}

// reBinding matches the megakernel's resource declarations. naga names each
// Metal kernel argument after the WGSL global it came from, and it numbers
// those arguments in signature order rather than by binding, so this map is the
// only link from a generated MSL argument back to a @group(0) @binding(n).
var reBinding = regexp.MustCompile(`@group\(0\)\s+@binding\((\d+)\)\s+var(?:<[^>]*>)?\s+(\w+)\s*:`)

// BindingNames maps each resource's WGSL name to its binding number, read from
// the linked shader itself so it cannot fall out of step with it.
func BindingNames() map[string]uint32 {
	out := map[string]uint32{}
	for _, m := range reBinding.FindAllStringSubmatch(shaders.LinkedWGSL(), -1) {
		var n uint32
		if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil {
			out[m[2]] = n
		}
	}
	return out
}

// blockerTreeRoot mirrors blocker_bvh_any_hit's
//
//	blocker_off = select(bvh_node_count, blocker_section_start, inst_count > 0)
//
// A scene without instancing appends its blocker tree straight after the main
// one and has no separate section start. Using blockerSecStart unconditionally
// points at node zero, which is the main tree's root, so the blocker structure
// silently fills with view geometry.
func blockerTreeRoot(c *sceneCache) uint32 {
	if len(c.instPlacements) > 0 {
		return c.blockerSecStart
	}
	return c.bvhNodeCount
}
