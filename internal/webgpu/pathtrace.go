// pathtrace.go — experimental Monte Carlo path tracer.
//
// A second compute kernel that shares the megakernel's scene buffers and
// nothing else. It exists to answer a feasibility question (docs/path-tracer.md):
// what does an honest, trick-free light transport model cost and look like on
// this hardware, at this resolution, on these scenes?
//
// It is additive by construction. No existing file changes, no existing shader
// module changes, and the megakernel's pipelines and bind group are untouched:
// this builds its own bind group over the same buffers, so the scene upload
// path is shared and the geometry is provably identical.
package webgpu

import (
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/vec"
	"raytracer/internal/webgpu/shaders"

	"github.com/rajveermalviya/go-webgpu/wgpu"
)

const (
	// PTParams in pt.wesl: 20 scalar words then four vec4s.
	ptParamsSize  = 160
	ptAccumStride = 16 // vec4<f32> per pixel
	// Per-pixel regions inside the scratch arena; must match PT_REGION_COUNT.
	ptScratchRegions = 11
	// Uniform slots are addressed with dynamic offsets so the whole frame —
	// trace, temporal, every a-trous iteration, resolve — fits in one command
	// buffer. queue.WriteBuffer only orders against submits, so without this
	// each iteration would need its own submit and its own round trip.
	ptUniformAlign = 256
	ptSlots        = 12
	// Reconstruction stage bits; must match PT_DN_* in pt.wesl.
	ptDenoiseTemporal = 1
	ptDenoiseAtrous   = 2
)

// PT flag bits. These are written out rather than derived with iota, and
// TestPTFlagsMatchShader checks every one against pt.wesl.
//
// They were iota-derived once. Inserting a new flag in the middle then shifted
// every later bit by one while the shader kept its literals, so four flags
// silently swapped meanings: -no-emitter-nee set WHITE_NOISE, -restir set
// NO_EMITTER_NEE, -debug indirect set RESTIR_TEMPORAL. Nothing errored and
// every render still looked plausible. The measurements taken through those
// flags were measuring the wrong thing, and the only reason it surfaced was an
// A/B that came back byte-identical when it should not have.
const (
	ptFlagPhysical       = 1
	ptFlagAmbient        = 2
	ptFlagNoNEE          = 4
	ptFlagNoSky          = 8
	ptFlagDbgAlbedo      = 16
	ptFlagDbgNormal      = 32
	ptFlagDbgDirect      = 64
	ptFlagNoShadow       = 128
	ptFlagAllLights      = 256
	ptFlagWhiteNoise     = 512
	ptFlagNoEmitterNEE   = 1024
	ptFlagRestirTemporal = 2048
	ptFlagDbgIndirect    = 4096
)

// PTDebug selects a first-hit diagnostic view instead of full transport.
type PTDebug int

const (
	PTDebugOff PTDebug = iota
	PTDebugAlbedo
	PTDebugNormal
	PTDebugDirect
	PTDebugIndirect
)

// PTOptions configures the transport model. The defaults reproduce the scenes'
// authored light falloff, because every scene in scenes/ is tuned against
// trace.wesl's non-physical 1/(0.5 + 0.08d²) attenuation; Physical swaps in
// inverse-square, which is correct and makes every existing scene wrong.
type PTOptions struct {
	MaxDepth      uint32
	RRDepth       uint32
	Physical      bool
	NoNEE         bool
	NoSky         bool
	LightScale    float32
	ClampIndirect float32
	// LightSamples is next-event estimation samples per shading vertex. One is
	// the honest path tracer; more trades rays for less light-selection noise,
	// which is the knob that matters most on a scene with hundreds of lights.
	LightSamples uint32
	// NoShadow and AllLights are ablations, matching gpuprof's feature matrix.
	NoShadow  bool
	AllLights bool
	// NoEmitterNEE leaves emissive geometry to BSDF sampling alone, as it was
	// before emitter next-event estimation. The A/B this exists for is in
	// docs/path-tracer.md.
	NoEmitterNEE bool
	// RISCandidates is how many emitters resampling considers per shading
	// vertex before committing its one shadow ray. One reproduces uniform
	// selection exactly.
	RISCandidates uint32
	// RestirTemporal reuses the previous frame's reservoir at the primary
	// hit, so a handful of candidates per frame behaves like many more.
	RestirTemporal bool
	// WhiteNoise falls back to the PCG sampler this shipped with, so the
	// Owen-scrambled Sobol sequence can be measured against it.
	WhiteNoise bool
	// Ambient restores the scene's hemispheric ambient constant at the first
	// diffuse vertex. Not physical; an A/B affordance for scenes authored
	// against it. See PT_FLAG_AMBIENT in pt.wesl.
	Ambient bool

	// Reconstruction. Temporal reprojection reuses the previous frame where
	// the geometry agrees; the a-trous passes filter what is left. Together
	// they are what makes one sample per pixel watchable, and they are the
	// reason a real-time path tracer does not need to converge.
	Temporal      bool
	Atrous        bool
	AtrousPasses  int
	PhiNormal     float32
	PhiDepth      float32
	PhiLum        float32
	TemporalAlpha float32
	// MaxHistory bounds the running mean. Capping it low (SVGF ships 32) keeps
	// a moving camera responsive but also stops a still one from ever
	// converging, which reads as a permanently washed-out image.
	MaxHistory float32
	// AtrousFade is the history length at which spatial filtering reaches
	// zero. Nonzero means a settled camera ends on the raw estimator's own
	// grain rather than on a blurred version of it. Zero filters always.
	AtrousFade float32
	Debug      PTDebug
}

// DefaultPTOptions returns settings that keep the scenes viewable as authored.
//
// LightScale defaults to pi because trace.wesl's direct term is
// albedo·N·L·attenuation with no 1/pi, while this tracer uses a physically
// consistent Lambert BRDF of albedo/pi. The factor makes direct lighting match
// the megakernel's brightness exactly, so what you see added on top is
// genuinely the indirect light and nothing else.
func DefaultPTOptions() PTOptions {
	return PTOptions{
		MaxDepth:       8,
		RRDepth:        3,
		LightScale:     3.14159265358979,
		ClampIndirect:  12,
		LightSamples:   1,
		AtrousPasses:   5,
		PhiNormal:      64,
		PhiDepth:       0.05,
		PhiLum:         4,
		TemporalAlpha:  0,
		MaxHistory:     512,
		AtrousFade:     24,
		RISCandidates:  16,
		RestirTemporal: false,
	}
}

func (o PTOptions) denoiseBits() uint32 {
	var d uint32
	if o.Temporal {
		d |= ptDenoiseTemporal
	}
	if o.Atrous {
		d |= ptDenoiseAtrous
	}
	return d
}

func (o PTOptions) flags() uint32 {
	var f uint32
	if o.Physical {
		f |= ptFlagPhysical
	}
	if o.NoNEE {
		f |= ptFlagNoNEE
	}
	if o.NoSky {
		f |= ptFlagNoSky
	}
	if o.Ambient {
		f |= ptFlagAmbient
	}
	if o.NoShadow {
		f |= ptFlagNoShadow
	}
	if o.AllLights {
		f |= ptFlagAllLights
	}
	if o.NoEmitterNEE {
		f |= ptFlagNoEmitterNEE
	}
	if o.RestirTemporal {
		f |= ptFlagRestirTemporal
	}
	if o.WhiteNoise {
		f |= ptFlagWhiteNoise
	}
	switch o.Debug {
	case PTDebugAlbedo:
		f |= ptFlagDbgAlbedo
	case PTDebugNormal:
		f |= ptFlagDbgNormal
	case PTDebugDirect:
		f |= ptFlagDbgDirect
	case PTDebugIndirect:
		f |= ptFlagDbgIndirect
	}
	return f
}

// PathTracer accumulates samples into its own buffer and resolves them on
// demand, so an image can be built from any number of dispatches.
type PathTracer struct {
	r *Renderer

	layout     *wgpu.BindGroupLayout
	pipeLayout *wgpu.PipelineLayout
	bind       *wgpu.BindGroup
	ptParams   *wgpu.Buffer

	// scratch holds every per-pixel region (accumulator, G-buffer, history,
	// ping-pong) in one binding; emitTable holds the emitter records and the
	// primitive->emitter map. Metal caps a stage at 31 buffers and naga takes
	// one for array sizes, so these cannot be separate bindings.
	scratch    *wgpu.Buffer
	emitTable  *wgpu.Buffer
	emitTables ptEmitterTables

	clearPipe    *wgpu.ComputePipeline
	mainPipe     *wgpu.ComputePipeline
	temporalPipe *wgpu.ComputePipeline
	atrousPipe   *wgpu.ComputePipeline
	resolvePipe  *wgpu.ComputePipeline

	// parity selects which half of the double-buffered G-buffer this frame
	// writes; the temporal pass reads the other half.
	probeCount   uint32
	probeSamples uint32

	parity   uint32
	havePrev bool
	prevPos  vec.V
	prevFwd  vec.V
	prevRt   vec.V
	prevUp   vec.V

	opts    PTOptions
	samples uint32
	frame   uint32
	gpuTime time.Duration
}

var ptBindingRe = regexp.MustCompile(`@group\(0\)\s*@binding\((\d+)\)`)

// NewPathTracer builds the path tracing pipelines over r's existing scene
// buffers. r must already be initialized; the scene is uploaded by Accumulate.
func NewPathTracer(r *Renderer, opts PTOptions) (*PathTracer, error) {
	if r == nil {
		return nil, fmt.Errorf("path tracer: nil renderer")
	}
	pt := &PathTracer{r: r, opts: opts}

	src := shaders.PathTraceSource()

	var err error
	pt.ptParams, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "pt params",
		Usage: wgpu.BufferUsage_Uniform | wgpu.BufferUsage_CopyDst,
		Size:  ptUniformAlign * ptSlots,
	})
	if err != nil {
		return nil, fmt.Errorf("create pt params buffer: %w", err)
	}
	pt.scratch, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "pt scratch arena",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst | wgpu.BufferUsage_CopySrc,
		Size:  uint64(r.maxDim*r.maxDim) * ptAccumStride * ptScratchRegions,
	})
	if err != nil {
		return nil, fmt.Errorf("create pt scratch buffer: %w", err)
	}

	// The kernel is tree-shaken from pt.wesl, so which of the megakernel's
	// bindings survive depends on what the transport code actually touches.
	// Reading the set back out of the linked WGSL keeps the layout correct
	// without a hand-maintained list that silently rots.
	pt.emitTable, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "pt emitter table",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxPrims * (ptEmitterStride + 4),
	})
	if err != nil {
		return nil, fmt.Errorf("create pt emitter table buffer: %w", err)
	}

	used := map[uint32]bool{}
	for _, m := range ptBindingRe.FindAllStringSubmatch(src, -1) {
		var n uint32
		fmt.Sscanf(m[1], "%d", &n)
		used[n] = true
	}

	avail := pt.bindings()
	var nums []uint32
	for n := range used {
		if _, ok := avail[n]; !ok {
			return nil, fmt.Errorf("path tracer shader uses unknown binding %d", n)
		}
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })

	entries := make([]wgpu.BindGroupLayoutEntry, 0, len(nums))
	groups := make([]wgpu.BindGroupEntry, 0, len(nums))
	for _, n := range nums {
		b := avail[n]
		typ := wgpu.BufferBindingType_ReadOnlyStorage
		switch {
		case b.uniform:
			typ = wgpu.BufferBindingType_Uniform
		case b.writable:
			typ = wgpu.BufferBindingType_Storage
		}
		entries = append(entries, wgpu.BindGroupLayoutEntry{
			Binding:    n,
			Visibility: wgpu.ShaderStage_Compute,
			Buffer: wgpu.BufferBindingLayout{
				Type:             typ,
				MinBindingSize:   b.min,
				HasDynamicOffset: b.dynamic,
			},
		})
		groups = append(groups, wgpu.BindGroupEntry{Binding: n, Buffer: b.buf, Size: b.size})
	}

	pt.layout, err = r.device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label:   "pt bind group layout",
		Entries: entries,
	})
	if err != nil {
		return nil, fmt.Errorf("create pt bind group layout: %w", err)
	}
	pt.pipeLayout, err = r.device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label:            "pt pipeline layout",
		BindGroupLayouts: []*wgpu.BindGroupLayout{pt.layout},
	})
	if err != nil {
		return nil, fmt.Errorf("create pt pipeline layout: %w", err)
	}
	pt.bind, err = r.device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Label:   "pt bind group",
		Layout:  pt.layout,
		Entries: groups,
	})
	if err != nil {
		return nil, fmt.Errorf("create pt bind group: %w", err)
	}

	mod, err := r.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label:          "pt shader",
		WGSLDescriptor: &wgpu.ShaderModuleWGSLDescriptor{Code: src},
	})
	if err != nil {
		return nil, fmt.Errorf("create pt shader module: %w", err)
	}
	defer mod.Release()

	for _, p := range []struct {
		name string
		dst  **wgpu.ComputePipeline
	}{
		{"pt_clear", &pt.clearPipe},
		{"pt_main", &pt.mainPipe},
		{"pt_temporal", &pt.temporalPipe},
		{"pt_atrous", &pt.atrousPipe},
		{"pt_resolve", &pt.resolvePipe},
	} {
		pipe, err := r.device.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{
			Label:   "pt " + p.name,
			Layout:  pt.pipeLayout,
			Compute: wgpu.ProgrammableStageDescriptor{Module: mod, EntryPoint: p.name},
		})
		if err != nil {
			return nil, fmt.Errorf("create %s pipeline: %w", p.name, err)
		}
		*p.dst = pipe
	}
	return pt, nil
}

type ptBinding struct {
	buf      *wgpu.Buffer
	size     uint64
	min      uint64
	uniform  bool
	writable bool
	dynamic  bool
}

// bindings mirrors the megakernel's bind group, plus the two slots this kernel
// adds. Sizes match device.go entry for entry so the two bind groups describe
// the same buffers identically.
func (pt *PathTracer) bindings() map[uint32]ptBinding {
	r := pt.r
	px := uint64(r.maxDim*r.maxDim) * 4
	return map[uint32]ptBinding{
		0:  {buf: r.params, size: paramsSize, min: paramsSize, uniform: true},
		1:  {buf: r.output, size: px, min: px, writable: true},
		2:  {buf: r.prims, size: maxPrims * primStride, min: primStride},
		3:  {buf: r.lights, size: maxLights * lightStride, min: lightStride},
		4:  {buf: r.blockers, size: maxPrims * primStride, min: primStride},
		5:  {buf: r.bvhNodes, size: maxBVHNodes * 4 * nodeStride, min: nodeStride},
		6:  {buf: r.terrains, size: maxTerrains * terrainStride, min: terrainStride},
		7:  {buf: r.samples, size: maxTerrainVals * 16, min: 16},
		8:  {buf: r.waters, size: maxWaters * waterStride, min: waterStride},
		9:  {buf: r.perm, size: permCount * 4, min: 4},
		10: {buf: r.aoVolume, size: r.aoVolumeFloats * 4, min: 4, writable: true},
		11: {buf: r.campfires, size: maxCampfires * campfireStride, min: campfireStride},
		12: {buf: r.holes, size: maxHoles * holeStride, min: holeStride},
		13: {buf: r.captures, size: r.captureBytes, min: 4},
		14: {buf: r.instTmpl, size: maxInstTemplates * instTemplateStride, min: instTemplateStride},
		15: {buf: r.instRecs, size: maxInstances * instanceStride, min: instanceStride},
		16: {buf: r.profile, size: profileCounterBytes, min: profileCounterBytes, writable: true},
		17: {buf: r.idxTables, size: idxTablesWords * 4, min: 4},
		19: {buf: r.documents, size: r.documentBytes, min: 4},
		20: {buf: r.boxFaces, size: maxPrims * boxFacesPerPrim * 4, min: 4},
		21: {buf: r.terrFeat, size: maxTerrainFeatures * terrainFeatureStride, min: terrainFeatureStride},
		22: {buf: r.terrPads, size: maxTerrainPads * terrainPadStride, min: terrainPadStride},
		23: {buf: r.terrMips, size: maxTerrainMipVals * 8, min: 8},
		26: {buf: r.terrZones, size: maxTerrainZones * terrainZoneStride, min: terrainZoneStride},
		27: {buf: r.terrZVerts, size: maxTerrainZoneVerts * terrainZoneVertStride, min: terrainZoneVertStride},
		30: {buf: pt.ptParams, size: ptParamsSize, min: ptParamsSize, uniform: true, dynamic: true},
		31: {buf: pt.scratch, size: uint64(r.maxDim*r.maxDim) * ptAccumStride * ptScratchRegions, min: ptAccumStride, writable: true},
		32: {buf: pt.emitTable, size: maxPrims * (ptEmitterStride + 4), min: 4},
	}
}

// Options returns the current transport settings.
func (pt *PathTracer) Options() PTOptions { return pt.opts }

// SetOptions changes the transport settings. The caller must Reset afterwards:
// samples already accumulated were drawn from the old model and averaging the
// two would be meaningless.
func (pt *PathTracer) SetOptions(o PTOptions) { pt.opts = o }

// Samples reports how many samples per pixel are currently accumulated.
func (pt *PathTracer) Samples() uint32 { return pt.samples }

// GPUTime reports the wall time of the most recent Accumulate dispatch.
func (pt *PathTracer) GPUTime() time.Duration { return pt.gpuTime }

// atrousPasses is the effective iteration count, bounded by the uniform slots
// reserved for them.
func (pt *PathTracer) atrousPasses() int {
	if !pt.opts.Atrous {
		return 0
	}
	n := pt.opts.AtrousPasses
	if n < 1 {
		n = 1
	}
	if n > ptSlots-2 {
		n = ptSlots - 2
	}
	return n
}

// finalSlot is the ping-pong half holding the filtered result. Each a-trous
// pass reads one half and writes the other, so after n passes the answer is in
// half n%2.
func (pt *PathTracer) finalSlot() uint32 {
	return uint32(pt.atrousPasses() % 2)
}

func (pt *PathTracer) slotBytes(spp, atrousIn, atrousStep uint32) []byte {
	b := make([]byte, ptParamsSize)
	putU32(b[0:4], spp)
	putU32(b[4:8], pt.opts.MaxDepth)
	putU32(b[8:12], pt.frame)
	putU32(b[12:16], pt.samples)
	putU32(b[16:20], pt.opts.flags())
	putF32(b[20:24], pt.opts.LightScale)
	putF32(b[24:28], pt.opts.ClampIndirect)
	putU32(b[28:32], pt.opts.RRDepth)
	putU32(b[32:36], pt.opts.LightSamples)
	putU32(b[36:40], pt.parity)
	putU32(b[40:44], pt.opts.denoiseBits())
	putU32(b[44:48], atrousStep)
	putU32(b[48:52], atrousIn)
	putF32(b[52:56], pt.opts.PhiNormal)
	putF32(b[56:60], pt.opts.PhiDepth)
	putF32(b[60:64], pt.opts.PhiLum)
	putF32(b[64:68], pt.opts.TemporalAlpha)
	putF32(b[68:72], pt.opts.MaxHistory)
	putF32(b[72:76], pt.opts.AtrousFade)
	putU32(b[76:80], pt.emitTables.count)
	// 68..80 padding, so the vec4s below start 16-byte aligned.
	prevPos, prevFwd, prevRt, prevUp := pt.prevPos, pt.prevFwd, pt.prevRt, pt.prevUp
	putVec4(b[80:96], prevPos)
	putVec4(b[96:112], prevFwd)
	putVec4(b[112:128], prevRt)
	putVec4(b[128:144], prevUp)
	putU32(b[144:148], pt.opts.RISCandidates)
	putU32(b[148:152], pt.probeCount)
	putU32(b[152:156], pt.probeSamples)
	return b
}

// writeSlots fills every uniform slot the frame will address: one for the
// trace and temporal passes, one per a-trous iteration, one for resolve.
// syncEmitters rebuilds and uploads the emitter tables when the packed scene
// changes. The tables are small and the scene is static in practice, so this
// is a signature check rather than a per-frame upload of 128 KB.
func (pt *PathTracer) syncEmitters(p renderParams) error {
	t := buildPTEmitters(p.prims, p.instTemplates)
	if t.sig == pt.emitTables.sig && pt.emitTables.count > 0 {
		return nil
	}
	if err := pt.r.wb(pt.emitTable, 0, t.records); err != nil {
		return err
	}
	if err := pt.r.wb(pt.emitTable, uint64(len(t.records)), t.index); err != nil {
		return err
	}
	pt.emitTables = t
	return nil
}

func (pt *PathTracer) writeSlots(spp uint32) error {
	n := pt.atrousPasses()
	buf := make([]byte, ptUniformAlign*ptSlots)
	copy(buf[0:], pt.slotBytes(spp, 0, 0))
	for i := 0; i < n; i++ {
		copy(buf[ptUniformAlign*(1+i):], pt.slotBytes(spp, uint32(i%2), uint32(1)<<uint(i)))
	}
	copy(buf[ptUniformAlign*(1+n):], pt.slotBytes(spp, pt.finalSlot(), 0))
	return pt.r.wb(pt.ptParams, 0, buf)
}

func slotOffset(i int) uint32 { return uint32(ptUniformAlign * i) }

func (pt *PathTracer) wait(sub wgpu.SubmissionIndex) {
	wrapped := wgpu.WrappedSubmissionIndex{Queue: pt.r.queue, SubmissionIndex: sub}
	pt.r.device.Poll(true, &wrapped)
}

func (pt *PathTracer) groups() (uint32, uint32) {
	return uint32((pt.r.w + workgroupXY - 1) / workgroupXY),
		uint32((pt.r.h + workgroupXY - 1) / workgroupXY)
}

// Reset clears the accumulator and the reconstruction history. Call it
// whenever the scene changes; camera motion does not need it once temporal
// reprojection is on, which is the point of the reprojection.
func (pt *PathTracer) Reset(cam *camera.Camera, v *render.View) error {
	p := pt.r.buildRenderParams(v)
	if err := pt.r.uploadFrame(cam, p, pt.r.w, pt.r.h); err != nil {
		return err
	}
	if err := pt.syncEmitters(p); err != nil {
		return err
	}
	pt.samples, pt.frame, pt.parity, pt.havePrev = 0, 0, 0, false
	if err := pt.writeSlots(0); err != nil {
		return err
	}
	enc, err := pt.r.device.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "pt clear encoder"})
	if err != nil {
		return err
	}
	defer enc.Release()
	pass := enc.BeginComputePass(&wgpu.ComputePassDescriptor{Label: "pt clear pass"})
	pass.SetPipeline(pt.clearPipe)
	pass.SetBindGroup(0, pt.bind, []uint32{slotOffset(0)})
	gx, gy := pt.groups()
	pass.DispatchWorkgroups(gx, gy, 1)
	if err := pass.End(); err != nil {
		pass.Release()
		return err
	}
	pass.Release()
	cmd, err := enc.Finish(&wgpu.CommandBufferDescriptor{Label: "pt clear cmd"})
	if err != nil {
		return err
	}
	defer cmd.Release()
	pt.wait(pt.r.queue.Submit(cmd))
	return nil
}

// Accumulate traces spp more samples and, when reconstruction is enabled, runs
// the temporal and a-trous passes over them. Every dispatch shares one command
// buffer: consecutive dispatches inside a compute pass see each other's
// storage writes, so no barrier or intermediate submit is needed.
func (pt *PathTracer) Accumulate(cam *camera.Camera, v *render.View, spp uint32) error {
	if spp == 0 {
		return nil
	}
	p := pt.r.buildRenderParams(v)
	if err := pt.r.uploadFrame(cam, p, pt.r.w, pt.r.h); err != nil {
		return err
	}
	if err := pt.syncEmitters(p); err != nil {
		return err
	}
	// The temporal pass reprojects with the previous frame's basis; on the
	// first frame there is none, so it must find no history rather than
	// reproject against garbage.
	if !pt.havePrev {
		pt.prevPos = cam.Pos
		pt.prevFwd, pt.prevRt, pt.prevUp = cam.Basis()
	}
	if err := pt.writeSlots(spp); err != nil {
		return err
	}

	start := time.Now()
	enc, err := pt.r.device.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "pt frame encoder"})
	if err != nil {
		return err
	}
	defer enc.Release()
	pass := enc.BeginComputePass(&wgpu.ComputePassDescriptor{Label: "pt frame pass"})
	gx, gy := pt.groups()

	pass.SetPipeline(pt.mainPipe)
	pass.SetBindGroup(0, pt.bind, []uint32{slotOffset(0)})
	pass.DispatchWorkgroups(gx, gy, 1)

	if pt.opts.Temporal {
		pass.SetPipeline(pt.temporalPipe)
		pass.SetBindGroup(0, pt.bind, []uint32{slotOffset(0)})
		pass.DispatchWorkgroups(gx, gy, 1)
	}
	for i := 0; i < pt.atrousPasses(); i++ {
		pass.SetPipeline(pt.atrousPipe)
		pass.SetBindGroup(0, pt.bind, []uint32{slotOffset(1 + i)})
		pass.DispatchWorkgroups(gx, gy, 1)
	}
	if err := pass.End(); err != nil {
		pass.Release()
		return err
	}
	pass.Release()
	cmd, err := enc.Finish(&wgpu.CommandBufferDescriptor{Label: "pt frame cmd"})
	if err != nil {
		return err
	}
	defer cmd.Release()
	pt.wait(pt.r.queue.Submit(cmd))
	pt.gpuTime = time.Since(start)

	pt.samples += spp
	pt.frame++
	pt.parity = 1 - pt.parity
	pt.prevPos = cam.Pos
	pt.prevFwd, pt.prevRt, pt.prevUp = cam.Basis()
	pt.havePrev = true
	return nil
}

// Resolve remodulates by albedo, tonemaps, and reads the frame back as RGBA8.
func (pt *PathTracer) Resolve(buf []byte) error {
	// Resolve reads the G-buffer half the last frame wrote, which parity has
	// already flipped away from.
	pt.parity = 1 - pt.parity
	err := pt.resolve(buf)
	pt.parity = 1 - pt.parity
	return err
}

func (pt *PathTracer) resolve(buf []byte) error {
	if err := pt.writeSlots(0); err != nil {
		return err
	}
	enc, err := pt.r.device.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "pt resolve encoder"})
	if err != nil {
		return err
	}
	defer enc.Release()
	pass := enc.BeginComputePass(&wgpu.ComputePassDescriptor{Label: "pt resolve pass"})
	pass.SetPipeline(pt.resolvePipe)
	pass.SetBindGroup(0, pt.bind, []uint32{slotOffset(1 + pt.atrousPasses())})
	gx, gy := pt.groups()
	pass.DispatchWorkgroups(gx, gy, 1)
	if err := pass.End(); err != nil {
		pass.Release()
		return err
	}
	pass.Release()

	size := uint64(pt.r.w * pt.r.h * 4)
	if err := enc.CopyBufferToBuffer(pt.r.output, 0, pt.r.read, 0, size); err != nil {
		return err
	}
	cmd, err := enc.Finish(&wgpu.CommandBufferDescriptor{Label: "pt resolve cmd"})
	if err != nil {
		return err
	}
	defer cmd.Release()
	pt.wait(pt.r.queue.Submit(cmd))
	return pt.r.mapReadInto(pt.r.read, size, buf)
}

// Release frees the path tracer's own GPU objects. The renderer's buffers are
// not owned here and are left alone.
func (pt *PathTracer) Release() {
	for _, p := range []*wgpu.ComputePipeline{pt.clearPipe, pt.mainPipe, pt.resolvePipe} {
		if p != nil {
			p.Release()
		}
	}
	if pt.bind != nil {
		pt.bind.Release()
	}
	if pt.pipeLayout != nil {
		pt.pipeLayout.Release()
	}
	if pt.layout != nil {
		pt.layout.Release()
	}
	for _, b := range []*wgpu.Buffer{pt.scratch, pt.emitTable} {
		if b != nil {
			b.Release()
		}
	}
	if pt.ptParams != nil {
		pt.ptParams.Release()
	}
}

func f32From(b []byte) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b))
}
