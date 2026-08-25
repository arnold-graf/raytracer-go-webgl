package webgpu

import (
	"unsafe"

	"raytracer/internal/gpuscene"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	maxInstances      = 32768
	maxInstTemplates  = 64
	instanceStride    = 64
	instTemplateStride = 16

	bvhTagTLAS uint32 = 1

	// bvhStackSize mirrors BVH_STACK_SIZE in bvh.wesl.
	bvhStackSize = gpuscene.BVHStackSize
)

// GPUTemplateRecord holds BLAS roots and prim-buffer offsets for one template
// (mirrors TemplateRecord in trace.wgsl).
type GPUTemplateRecord struct {
	PrimBase        uint32
	BlockerBase     uint32
	BlasRoot        uint32
	BlockerBlasRoot uint32
}

// GPUInstanceRecord holds one TLAS placement transform (mirrors InstanceRecord).
type GPUInstanceRecord struct {
	Xf0        [4]float32
	Xf1        [4]float32
	Xf2        [4]float32
	TemplateID uint32
	_pad       [3]uint32
}

// instScenePack is the instancing section of a packed GPU scene.
type instScenePack struct {
	templates           []GPUTemplateRecord
	instances           []GPUInstanceRecord
	instNodeBase        uint32
	instNodeCount       uint32
	blockerSectionStart uint32
	blockerInstBase     uint32
	blockerInstCount    uint32
}

// PackInstancedScene exports packInstancedScene for tests.
func PackInstancedScene(s *scene.Scene) (prims []GPUPrimitive, ok bool) {
	prims, _, _, _, _, _, _, ok = packInstancedScene(s)
	return prims, ok
}

func packInstancedScene(s *scene.Scene) (
	prims, blockers []GPUPrimitive,
	nodes []GPUBVHNode,
	staticBVHCount, staticBlockerBVHCount uint32,
	isp instScenePack,
	dynGPU gpuIndexMap,
	ok bool,
) {
	cat := s.Instancing()
	static, haveStatic := s.StaticPrimitiveCounts()
	if cat == nil || len(cat.Placements) == 0 || !haveStatic {
		return nil, nil, nil, 0, 0, instScenePack{}, gpuIndexMap{}, false
	}

	staticScene := sliceStaticScene(s, static)
	prims = packPrimitivesOmitDynamic(staticScene, s)
	blockers = packBlockersOmitDynamic(staticScene, s)
	var dynMap gpuIndexMap
	var dynBlocker gpuIndexMap
	prims, dynMap = appendDynamicBodyPrimitives(s, prims)
	blockers, dynBlocker = appendDynamicBodyBlockers(s, blockers)
	dynMap.primToBlocker = linkPrimToBlocker(dynMap, dynBlocker)
	staticBVH := PackBVH(prims)
	staticBlkBVH := PackBVH(blockers)

	templates := make([]GPUTemplateRecord, len(cat.Templates))
	var primBase uint32 = uint32(len(prims))
	var blockerBase uint32 = uint32(len(blockers))
	tmplPrims := make([][]GPUPrimitive, len(cat.Templates))
	tmplBlockers := make([][]GPUPrimitive, len(cat.Templates))

	for i := range cat.Templates {
		if cat.Templates[i].Scene == nil {
			continue
		}
		tp := PackPrimitives(cat.Templates[i].Scene)
		tb := PackBlockers(cat.Templates[i].Scene)
		tmplPrims[i] = tp
		tmplBlockers[i] = tb
		templates[i].PrimBase = primBase
		templates[i].BlockerBase = blockerBase
		prims = append(prims, tp...)
		blockers = append(blockers, tb...)
		primBase += uint32(len(tp))
		blockerBase += uint32(len(tb))
	}

	instances := make([]GPUInstanceRecord, 0, len(cat.Placements))
	tlasRefs := make([]tlasRef, 0, len(cat.Placements))
	for _, pl := range cat.Placements {
		if pl.TemplateIndex < 0 || pl.TemplateIndex >= len(cat.Templates) {
			continue
		}
		t := cat.Templates[pl.TemplateIndex]
		if t.Scene == nil {
			continue
		}
		wmin, wmax, boundsOK := scene.TemplateWorldBounds(t.Scene, pl.Xform)
		if !boundsOK {
			continue
		}
		rec := GPUInstanceRecord{TemplateID: uint32(pl.TemplateIndex)}
		setInstanceXform(&rec, pl.Xform)
		instances = append(instances, rec)
		tlasRefs = append(tlasRefs, tlasRef{
			idx:      uint32(len(instances) - 1),
			min:      wmin,
			max:      wmax,
			centroid: wmin.Add(wmax).Scale(0.5),
		})
	}

	// Primary: [static BVH | TLAS | template BLAS…]
	nodes = append([]GPUBVHNode(nil), staticBVH...)
	isp.instNodeBase = uint32(len(nodes))
	tlasNodes := packTLAS(tlasRefs)
	for j := range tlasNodes {
		offsetTLASNode(&tlasNodes[j], isp.instNodeBase)
	}
	nodes = append(nodes, tlasNodes...)
	isp.instNodeCount = uint32(len(tlasNodes))

	for ti := range cat.Templates {
		if cat.Templates[ti].Scene == nil {
			continue
		}
		root := uint32(len(nodes))
		templates[ti].BlasRoot = root
		blas := PackBVH(tmplPrims[ti])
		base := templates[ti].PrimBase
		for j := range blas {
			offsetBLASNode(&blas[j], base, root)
			nodes = append(nodes, blas[j])
		}
	}
	isp.instNodeCount = uint32(len(nodes)) - isp.instNodeBase

	isp.blockerSectionStart = uint32(len(nodes))
	blockerSection := append([]GPUBVHNode(nil), staticBlkBVH...)
	isp.blockerInstBase = isp.blockerSectionStart + uint32(len(staticBlkBVH))
	blkTLAS := packTLAS(tlasRefs)
	for j := range blkTLAS {
		offsetTLASNode(&blkTLAS[j], isp.blockerInstBase)
	}
	blockerSection = append(blockerSection, blkTLAS...)

	for ti := range cat.Templates {
		if cat.Templates[ti].Scene == nil {
			continue
		}
		root := isp.blockerSectionStart + uint32(len(blockerSection))
		templates[ti].BlockerBlasRoot = root
		bblas := PackBVH(tmplBlockers[ti])
		base := templates[ti].BlockerBase
		for j := range bblas {
			offsetBLASNode(&bblas[j], base, root)
			blockerSection = append(blockerSection, bblas[j])
		}
	}
	isp.blockerInstCount = uint32(len(blockerSection)) - uint32(len(staticBlkBVH))
	nodes = append(nodes, blockerSection...)

	isp.templates = templates
	isp.instances = instances

	staticBVHCount = uint32(len(staticBVH))
	staticBlockerBVHCount = uint32(len(staticBlkBVH))
	return prims, blockers, nodes, staticBVHCount, staticBlockerBVHCount, isp, dynMap, true
}

func offsetBLASNode(n *GPUBVHNode, primBase, root uint32) {
	if n.Info[3] > 0 && n.Info[3] <= 2 {
		n.Info[0] += primBase
		if n.Info[3] > 1 {
			n.Info[1] += primBase
		}
		return
	}
	n.Info[0] += root
	n.Info[1] += root
}

func offsetTLASNode(n *GPUBVHNode, base uint32) {
	if n.Info[2] == bvhTagTLAS && n.Info[3] == 0 {
		n.Info[0] += base
		n.Info[1] += base
	}
}

func setInstanceXform(rec *GPUInstanceRecord, x *scene.Transform) {
	if rec == nil {
		return
	}
	r0, r1, r2, t := x.GPUData()
	rec.Xf0 = [4]float32{f(r0.X), f(r0.Y), f(r0.Z), f(t.X)}
	rec.Xf1 = [4]float32{f(r1.X), f(r1.Y), f(r1.Z), f(t.Y)}
	rec.Xf2 = [4]float32{f(r2.X), f(r2.Y), f(r2.Z), f(t.Z)}
}

func sliceStaticScene(s *scene.Scene, static scene.PrimitiveCounts) *scene.Scene {
	return &scene.Scene{
		Spheres:   append([]scene.Sphere(nil), s.Spheres[:min(static.Spheres, len(s.Spheres))]...),
		Planes:    append([]scene.Plane(nil), s.Planes...),
		Boxes:     append([]scene.Box(nil), s.Boxes[:min(static.Boxes, len(s.Boxes))]...),
		Cylinders: append([]scene.Cylinder(nil), s.Cylinders[:min(static.Cylinders, len(s.Cylinders))]...),
		Cones:     append([]scene.Cone(nil), s.Cones[:min(static.Cones, len(s.Cones))]...),
		Tori:      append([]scene.Torus(nil), s.Tori[:min(static.Tori, len(s.Tori))]...),
		Rings:     append([]scene.Ring(nil), s.Rings[:min(static.Rings, len(s.Rings))]...),
		Lenses:    append([]scene.Lens(nil), s.Lenses[:min(static.Lenses, len(s.Lenses))]...),
	}
}

type tlasRef struct {
	idx      uint32
	min, max vec.V
	centroid vec.V
}

func packTLAS(refs []tlasRef) []GPUBVHNode {
	if len(refs) == 0 {
		return nil
	}
	b := &tlasBuilder{refs: refs, nodes: make([]GPUBVHNode, 0, len(refs)*2)}
	b.buildRange(0, len(refs))
	return b.nodes
}

type tlasBuilder struct {
	refs  []tlasRef
	nodes []GPUBVHNode
}

func (b *tlasBuilder) buildRange(start, end int) uint32 {
	ni := uint32(len(b.nodes))
	b.nodes = append(b.nodes, GPUBVHNode{})

	bmin, bmax := b.refs[start].min, b.refs[start].max
	for i := start + 1; i < end; i++ {
		bmin = minVec(bmin, b.refs[i].min)
		bmax = maxVec(bmax, b.refs[i].max)
	}

	if end-start <= 1 {
		b.nodes[ni] = GPUBVHNode{
			Min:  vec4(bmin),
			Max:  vec4(bmax),
			Info: [4]uint32{b.refs[start].idx, 0, bvhTagTLAS, 1},
		}
		return ni
	}

	mid := (start + end) / 2
	left := b.buildRange(start, mid)
	right := b.buildRange(mid, end)
	b.nodes[ni] = GPUBVHNode{
		Min:  vec4(bmin),
		Max:  vec4(bmax),
		Info: [4]uint32{left, right, bvhTagTLAS, 0},
	}
	return ni
}

func instTemplateBytes(recs []GPUTemplateRecord) []byte {
	if len(recs) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&recs[0])), len(recs)*instTemplateStride)
}

func instanceBytes(recs []GPUInstanceRecord) []byte {
	if len(recs) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&recs[0])), len(recs)*instanceStride)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
