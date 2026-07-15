// Package webgpu contains the WebGPU renderer backend, the only renderer in the
// app. It packs the scene into GPU buffers and runs the path tracer as a compute
// shader (shaders/modules/*.wesl, linked via shaders.Source / go generate).
package webgpu

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
	"raytracer/internal/webgpu/shaders"

	"github.com/rajveermalviya/go-webgpu/wgpu"
)

const (
	fovScale    = 0.5773502691896257 // tan(60deg / 2)
	paramsSize  = 288
	workgroupXY = 8
	// Six square portal captures (see texture.MaxCaptureDim).
	maxCaptureDim = texture.MaxCaptureDim

	// ambientFlat is the CPU shade()'s flat-ambient term used when a scene has
	// no hemispheric sky/ground ambient: lit = albedo * 0.04.
	ambientFlat = 0.03
)

func maxCapturePixels(maxDim int) int {
	if maxDim <= 0 {
		maxDim = maxCaptureDim
	}
	if maxDim > maxCaptureDim {
		maxDim = maxCaptureDim
	}
	return maxDim * maxDim * 6
}

// Renderer is the early WebGPU backend: it dispatches a compute shader into a
// storage buffer, reads that buffer back, and lets the existing Ebiten app blit
// it. This readback path is deliberately temporary; the real WebGPU renderer
// will present directly to a surface once parity is useful.
type Renderer struct {
	w, h int
	// maxDim is max(w,h); output/read buffers are sized for maxDim² so square
	// portal captures can render at 1:1 aspect without a separate allocation.
	maxDim int

	instance *wgpu.Instance
	adapter  *wgpu.Adapter
	device   *wgpu.Device
	queue    *wgpu.Queue

	params    *wgpu.Buffer
	prims     *wgpu.Buffer
	blockers  *wgpu.Buffer
	lights    *wgpu.Buffer
	bvhNodes  *wgpu.Buffer
	terrains  *wgpu.Buffer
	samples   *wgpu.Buffer
	terrFeat  *wgpu.Buffer
	terrPads  *wgpu.Buffer
	waters    *wgpu.Buffer
	perm      *wgpu.Buffer
	aoVolume  *wgpu.Buffer
	campfires *wgpu.Buffer
	holes     *wgpu.Buffer
	captures  *wgpu.Buffer
	documents *wgpu.Buffer
	boxFaces  *wgpu.Buffer
	instTmpl  *wgpu.Buffer
	instRecs  *wgpu.Buffer
	planeIdx  *wgpu.Buffer
	blkPlane  *wgpu.Buffer
	output    *wgpu.Buffer
	read      *wgpu.Buffer
	pipeline  *wgpu.ComputePipeline
	bind      *wgpu.BindGroup

	// Frame pipelining: when enabled (SetPipelined), Render submits the current
	// frame and hands back the previous frame's pixels, so the CPU packs frame
	// N+1 while the GPU renders frame N instead of stalling on it. reads[] are
	// two readback staging buffers ping-ponged so one can be mapped while the
	// other receives the next copy. Portal captures and the profiling benchmark
	// keep using the synchronous path (r.read).
	pipelined    bool
	reads        [2]*wgpu.Buffer
	pipePending  bool
	pipeSlot     int
	pipeParity   int
	pipeSize     uint64
	pipeSub      wgpu.SubmissionIndex
	pipeProfiled bool
	// gpuTime is the last true GPU compute wall time (submit->idle), measured on
	// the synchronous workload-sample frames and carried on pipelined frames so
	// the HUD keeps reporting real GPU cost instead of the ~0 async submit time.
	gpuTime time.Duration
	// lastFrame retains the most recently presented pixels so the pipeline's cold
	// path (startup, or the frame after a synchronous sample) reuses them instead
	// of flashing black.
	lastFrame      []byte
	lastFrameReady bool

	cache  sceneCache  // memoized static scene buffers (see cache.go)
	timing FrameTiming // phase breakdown of the most recent Render (see profile.go)

	profiling       bool
	profileCounters GPUProfileCounters
	profile         *wgpu.Buffer
	profileRead     *wgpu.Buffer

	liveWorkload  bool
	workloadFrame int
	workload      render.GPUWorkload

	captureVer     uint64
	captureW       int
	captureH       int
	captureLoaded  bool
	captureBytes   uint64 // GPU buffer size for six square captures
	documentVer    uint64
	documentLoaded bool
	documentBytes  uint64
}

// InvalidateDocumentTextures forces document text textures to re-upload on the next frame.
func (r *Renderer) InvalidateDocumentTextures() {
	r.documentVer = 0
	r.documentLoaded = false
}

// SyncDocumentTextures uploads document text textures immediately. Used after
// scene reload so the next frame does not render with a stale document_loaded flag.
func (r *Renderer) SyncDocumentTextures() {
	r.documentVer = 0
	r.documentLoaded = false
	r.uploadDocumentTextures()
}

func (r *Renderer) uploadDocumentTextures() {
	ver := texture.DocumentGPUVersion()
	px, ok := texture.PackDocumentsGPU()
	if !ok || len(px)*4 > int(r.documentBytes) {
		return
	}
	if err := r.queue.WriteBuffer(r.documents, 0, u32Bytes(px)); err != nil {
		return
	}
	r.documentLoaded = true
	r.documentVer = ver
}

// New initializes WebGPU and compiles the skeleton sky compute pipeline.
func New(w, h int) (*Renderer, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid render size %dx%d", w, h)
	}
	r := &Renderer{w: w, h: h, maxDim: w}
	if h > r.maxDim {
		r.maxDim = h
	}
	r.captureBytes = uint64(maxCapturePixels(r.maxDim) * 4)
	r.documentBytes = uint64(texture.DocumentCount * texture.DocumentTexW * texture.DocumentTexH * 4)
	if err := r.init(); err != nil {
		r.Release()
		return nil, err
	}
	return r, nil
}

func (r *Renderer) init() error {
	r.instance = wgpu.CreateInstance(nil)
	var err error
	r.adapter, err = r.instance.RequestAdapter(&wgpu.RequestAdapterOptions{
		PowerPreference: wgpu.PowerPreference_HighPerformance,
	})
	if err != nil {
		return fmt.Errorf("request adapter: %w", err)
	}
	// Request the adapter's full supported limits so we can bind more than the
	// 8 default storage buffers (we currently need 11: prims, lights, blockers,
	// bvh, terrains, terrain samples, waters, perm table, AO volume, campfires
	// and the output buffer).
	supported := r.adapter.GetLimits().Limits
	r.device, err = r.adapter.RequestDevice(&wgpu.DeviceDescriptor{
		Label:          "raytracer webgpu skeleton",
		RequiredLimits: &wgpu.RequiredLimits{Limits: supported},
	})
	if err != nil {
		return fmt.Errorf("request device: %w", err)
	}
	r.queue = r.device.GetQueue()

	size := uint64(r.maxDim * r.maxDim * 4)
	r.params, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "trace params",
		Usage: wgpu.BufferUsage_Uniform | wgpu.BufferUsage_CopyDst,
		Size:  paramsSize,
	})
	if err != nil {
		return fmt.Errorf("create params buffer: %w", err)
	}
	r.prims, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "scene primitives",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxPrims * primStride,
	})
	if err != nil {
		return fmt.Errorf("create primitives buffer: %w", err)
	}
	r.blockers, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "shadow blockers",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxPrims * primStride,
	})
	if err != nil {
		return fmt.Errorf("create blockers buffer: %w", err)
	}
	r.lights, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "scene lights",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxLights * lightStride,
	})
	if err != nil {
		return fmt.Errorf("create lights buffer: %w", err)
	}
	r.bvhNodes, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "bvh nodes",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxBVHNodes * 4 * nodeStride,
	})
	if err != nil {
		return fmt.Errorf("create bvh nodes buffer: %w", err)
	}
	r.terrains, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "terrains",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxTerrains * terrainStride,
	})
	if err != nil {
		return fmt.Errorf("create terrains buffer: %w", err)
	}
	r.samples, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "terrain samples",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxTerrainVals * 16,
	})
	if err != nil {
		return fmt.Errorf("create terrain samples buffer: %w", err)
	}
	r.terrFeat, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "terrain features",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxTerrainFeatures * terrainFeatureStride,
	})
	if err != nil {
		return fmt.Errorf("create terrain features buffer: %w", err)
	}
	r.terrPads, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "terrain pads",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxTerrainPads * terrainPadStride,
	})
	if err != nil {
		return fmt.Errorf("create terrain pads buffer: %w", err)
	}
	r.waters, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "waters",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxWaters * waterStride,
	})
	if err != nil {
		return fmt.Errorf("create waters buffer: %w", err)
	}
	r.perm, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "perlin perm",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  permCount * 4,
	})
	if err != nil {
		return fmt.Errorf("create perm buffer: %w", err)
	}
	r.aoVolume, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "ao volume",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxAOFloats * 4,
	})
	if err != nil {
		return fmt.Errorf("create ao volume buffer: %w", err)
	}
	r.campfires, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "campfires",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxCampfires * campfireStride,
	})
	if err != nil {
		return fmt.Errorf("create campfires buffer: %w", err)
	}
	r.holes, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "box holes",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxHoles * holeStride,
	})
	if err != nil {
		return fmt.Errorf("create holes buffer: %w", err)
	}
	r.instTmpl, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "instance templates",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxInstTemplates * instTemplateStride,
	})
	if err != nil {
		return fmt.Errorf("create instance templates buffer: %w", err)
	}
	r.instRecs, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "instance records",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxInstances * instanceStride,
	})
	if err != nil {
		return fmt.Errorf("create instance records buffer: %w", err)
	}
	r.planeIdx, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "plane indices",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxPrims * 4,
	})
	if err != nil {
		return fmt.Errorf("create plane indices buffer: %w", err)
	}
	r.blkPlane, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "blocker plane indices",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxPrims * 4,
	})
	if err != nil {
		return fmt.Errorf("create blocker plane indices buffer: %w", err)
	}
	r.profile, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "profile counters",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopySrc | wgpu.BufferUsage_CopyDst,
		Size:  profileCounterBytes,
	})
	if err != nil {
		return fmt.Errorf("create profile buffer: %w", err)
	}
	r.profileRead, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "profile readback",
		Usage: wgpu.BufferUsage_MapRead | wgpu.BufferUsage_CopyDst,
		Size:  profileCounterBytes,
	})
	if err != nil {
		return fmt.Errorf("create profile readback buffer: %w", err)
	}
	r.captures, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "capture pixels",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  r.captureBytes,
	})
	if err != nil {
		return fmt.Errorf("create captures buffer: %w", err)
	}
	r.documents, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "document pixels",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  r.documentBytes,
	})
	if err != nil {
		return fmt.Errorf("create documents buffer: %w", err)
	}
	r.boxFaces, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "box face textures",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopyDst,
		Size:  maxPrims * boxFacesPerPrim * 4,
	})
	if err != nil {
		return fmt.Errorf("create box face textures buffer: %w", err)
	}
	// The permutation table is constant, so upload it once up front.
	if err := r.queue.WriteBuffer(r.perm, 0, u32Bytes(PackPerm())); err != nil {
		return fmt.Errorf("upload perm table: %w", err)
	}
	r.output, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "sky output",
		Usage: wgpu.BufferUsage_Storage | wgpu.BufferUsage_CopySrc,
		Size:  size,
	})
	if err != nil {
		return fmt.Errorf("create output buffer: %w", err)
	}
	r.read, err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "sky readback",
		Usage: wgpu.BufferUsage_MapRead | wgpu.BufferUsage_CopyDst,
		Size:  size,
	})
	if err != nil {
		return fmt.Errorf("create readback buffer: %w", err)
	}
	for i := range r.reads {
		r.reads[i], err = r.device.CreateBuffer(&wgpu.BufferDescriptor{
			Label: "pipelined readback",
			Usage: wgpu.BufferUsage_MapRead | wgpu.BufferUsage_CopyDst,
			Size:  size,
		})
		if err != nil {
			return fmt.Errorf("create pipelined readback buffer %d: %w", i, err)
		}
	}

	shader, err := r.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label:          "trace shader",
		WGSLDescriptor: &wgpu.ShaderModuleWGSLDescriptor{Code: shaders.Source()},
	})
	if err != nil {
		return fmt.Errorf("create shader module: %w", err)
	}
	defer shader.Release()

	layout, err := r.device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "trace bind group layout",
		Entries: []wgpu.BindGroupLayoutEntry{
			{
				Binding:    0,
				Visibility: wgpu.ShaderStage_Compute,
				Buffer: wgpu.BufferBindingLayout{
					Type:           wgpu.BufferBindingType_Uniform,
					MinBindingSize: paramsSize,
				},
			},
			{
				Binding:    1,
				Visibility: wgpu.ShaderStage_Compute,
				Buffer: wgpu.BufferBindingLayout{
					Type:           wgpu.BufferBindingType_Storage,
					MinBindingSize: size,
				},
			},
			{
				Binding:    2,
				Visibility: wgpu.ShaderStage_Compute,
				Buffer: wgpu.BufferBindingLayout{
					Type:           wgpu.BufferBindingType_ReadOnlyStorage,
					MinBindingSize: primStride,
				},
			},
			{
				Binding:    3,
				Visibility: wgpu.ShaderStage_Compute,
				Buffer: wgpu.BufferBindingLayout{
					Type:           wgpu.BufferBindingType_ReadOnlyStorage,
					MinBindingSize: lightStride,
				},
			},
			{
				Binding:    4,
				Visibility: wgpu.ShaderStage_Compute,
				Buffer: wgpu.BufferBindingLayout{
					Type:           wgpu.BufferBindingType_ReadOnlyStorage,
					MinBindingSize: primStride,
				},
			},
			{
				Binding:    5,
				Visibility: wgpu.ShaderStage_Compute,
				Buffer: wgpu.BufferBindingLayout{
					Type:           wgpu.BufferBindingType_ReadOnlyStorage,
					MinBindingSize: nodeStride,
				},
			},
			{Binding: 6, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: terrainStride}},
			{Binding: 7, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 16}},
			{Binding: 8, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: waterStride}},
			{Binding: 9, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 4}},
			{Binding: 10, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 4}},
			{Binding: 11, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: campfireStride}},
			{Binding: 12, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: holeStride}},
			{Binding: 13, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 4}},
			{Binding: 14, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: instTemplateStride}},
			{Binding: 15, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: instanceStride}},
			{Binding: 16, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_Storage, MinBindingSize: profileCounterBytes}},
			{Binding: 17, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 4}},
			{Binding: 18, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 4}},
			{Binding: 19, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 4}},
			{Binding: 20, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: 4}},
			{Binding: 21, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: terrainFeatureStride}},
			{Binding: 22, Visibility: wgpu.ShaderStage_Compute, Buffer: wgpu.BufferBindingLayout{Type: wgpu.BufferBindingType_ReadOnlyStorage, MinBindingSize: terrainPadStride}},
		},
	})
	if err != nil {
		return fmt.Errorf("create bind group layout: %w", err)
	}
	defer layout.Release()

	pipelineLayout, err := r.device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label:            "sky pipeline layout",
		BindGroupLayouts: []*wgpu.BindGroupLayout{layout},
	})
	if err != nil {
		return fmt.Errorf("create pipeline layout: %w", err)
	}
	defer pipelineLayout.Release()

	r.pipeline, err = r.device.CreateComputePipeline(&wgpu.ComputePipelineDescriptor{
		Label:  "sky pipeline",
		Layout: pipelineLayout,
		Compute: wgpu.ProgrammableStageDescriptor{
			Module:     shader,
			EntryPoint: "main",
		},
	})
	if err != nil {
		return fmt.Errorf("create compute pipeline: %w", err)
	}

	r.bind, err = r.device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Label:  "trace bind group",
		Layout: layout,
		Entries: []wgpu.BindGroupEntry{
			{Binding: 0, Buffer: r.params, Size: paramsSize},
			{Binding: 1, Buffer: r.output, Size: size},
			{Binding: 2, Buffer: r.prims, Size: maxPrims * primStride},
			{Binding: 3, Buffer: r.lights, Size: maxLights * lightStride},
			{Binding: 4, Buffer: r.blockers, Size: maxPrims * primStride},
			{Binding: 5, Buffer: r.bvhNodes, Size: maxBVHNodes * 4 * nodeStride},
			{Binding: 6, Buffer: r.terrains, Size: maxTerrains * terrainStride},
			{Binding: 7, Buffer: r.samples, Size: maxTerrainVals * 16},
			{Binding: 8, Buffer: r.waters, Size: maxWaters * waterStride},
			{Binding: 9, Buffer: r.perm, Size: permCount * 4},
			{Binding: 10, Buffer: r.aoVolume, Size: maxAOFloats * 4},
			{Binding: 11, Buffer: r.campfires, Size: maxCampfires * campfireStride},
			{Binding: 12, Buffer: r.holes, Size: maxHoles * holeStride},
			{Binding: 13, Buffer: r.captures, Size: r.captureBytes},
			{Binding: 14, Buffer: r.instTmpl, Size: maxInstTemplates * instTemplateStride},
			{Binding: 15, Buffer: r.instRecs, Size: maxInstances * instanceStride},
			{Binding: 16, Buffer: r.profile, Size: profileCounterBytes},
			{Binding: 17, Buffer: r.planeIdx, Size: maxPrims * 4},
			{Binding: 18, Buffer: r.blkPlane, Size: maxPrims * 4},
			{Binding: 19, Buffer: r.documents, Size: r.documentBytes},
			{Binding: 20, Buffer: r.boxFaces, Size: maxPrims * boxFacesPerPrim * 4},
			{Binding: 21, Buffer: r.terrFeat, Size: maxTerrainFeatures * terrainFeatureStride},
			{Binding: 22, Buffer: r.terrPads, Size: maxTerrainPads * terrainPadStride},
		},
	})
	if err != nil {
		return fmt.Errorf("create bind group: %w", err)
	}
	return nil
}

// Render writes the WebGPU-computed frame into buf. Phase 2 implements camera
// rays, analytic primitive intersection, flat-ambient diffuse shading, emissive
// passthrough and the clear sky on a miss. pixSize is ignored (the GPU always
// renders at full resolution).
func (r *Renderer) Render(buf []byte, cam *camera.Camera, v *render.View, _ int) {
	if len(buf) < r.w*r.h*4 {
		return
	}
	if cam == nil {
		return
	}
	packStart := time.Now()
	rp := r.buildRenderParams(v)
	r.timing = FrameTiming{
		Pack:     time.Since(packStart),
		Prims:    len(rp.prims),
		Blockers: len(rp.blockers),
		BVHNodes: len(rp.bvhNodes),
		Holes:    len(rp.holes),
	}
	// Workload-sample frames (rp.profileEnabled, ~once/sec) go through the
	// synchronous path so their submit->idle wall time gives a true GPU cost
	// reading; pipelined frames carry that value forward for the HUD.
	var err error
	if r.pipelined && !r.profiling && !rp.profileEnabled && r.lastFrameReady {
		err = r.renderPipelined(buf[:r.w*r.h*4], cam, rp)
	} else {
		// The synchronous path shares r.output/params with any in-flight
		// pipelined frame; drain it first so nothing is lost or double-mapped.
		r.discardPending()
		err = r.render(buf[:r.w*r.h*4], cam, rp, r.w, r.h)
	}
	if err != nil {
		for i := 0; i < r.w*r.h; i++ {
			o := i * 4
			buf[o], buf[o+1], buf[o+2], buf[o+3] = 255, 0, 255, 255
		}
		return
	}
	r.retainLastFrame(buf[:r.w*r.h*4])
}

// retainLastFrame snapshots the just-presented pixels so the pipeline's cold
// path can reuse them instead of showing black.
func (r *Renderer) retainLastFrame(buf []byte) {
	if len(r.lastFrame) != len(buf) {
		r.lastFrame = make([]byte, len(buf))
	}
	copy(r.lastFrame, buf)
	r.lastFrameReady = true
}

// RenderSquare fills buf (len ≥ size²×4) with a square 1:1-aspect frame. Used
// for portal capture textures mapped onto the cube's square walls.
func (r *Renderer) RenderSquare(buf []byte, size int, cam *camera.Camera, v *render.View) {
	if size <= 0 || size > r.maxDim || len(buf) < size*size*4 {
		return
	}
	if cam == nil {
		return
	}
	// Portal captures render synchronously into the shared output/params buffers;
	// drain any in-flight pipelined frame first so it isn't clobbered or lost.
	r.discardPending()
	rp := r.buildRenderParams(v)
	if err := r.render(buf[:size*size*4], cam, rp, size, size); err != nil {
		for i := 0; i < size*size; i++ {
			o := i * 4
			buf[o], buf[o+1], buf[o+2], buf[o+3] = 0x20, 0x20, 0x30, 0xff
		}
	}
}

func (r *Renderer) buildRenderParams(v *render.View) renderParams {
	uploadStatic := false
	uploadPartial := false
	timeSec := 0.0
	shadows := false
	mirror := false
	aoEnabled := false
	sky := 0
	var (
		bodyEnabled   bool
		bodyDir       vec.V
		bodyColor     vec.V
		bodyCosRadius float32
		bodyGlow      float32
	)
	if v != nil && v.Scene != nil {
		if !r.cache.fresh(v) {
			r.cache.rebuild(v)
			uploadStatic = true
		} else if !r.cache.transformsFresh(v) {
			r.cache.updateDynamicTransforms(v.Scene)
			uploadPartial = len(r.cache.partialPrimSpans) > 0 || len(r.cache.partialBlockerSpans) > 0
		} else if v.AOok && v.AOVersion != r.cache.aoVersion {
			r.cache.ao, r.cache.aoOK = PackAOVolume(v)
			r.cache.aoVersion = v.AOVersion
			uploadStatic = true
		}
		timeSec = v.Time
		shadows = v.Shadow
		mirror = v.Mirror
		aoEnabled = v.AO
		sky = v.Scene.Env.Sky
		if env := v.Scene.Env; env.Sun.Visible() && env.SunDir != (vec.V{}) {
			bodyEnabled = true
			bodyDir = env.SunDir.Scale(-1).Normalize()
			bodyColor = env.Sun.Color
			radius := env.Sun.Size * 0.5 * math.Pi / 180.0
			bodyCosRadius = float32(math.Cos(radius))
			bodyGlow = float32(env.Sun.Glow)
		}
	}
	c := &r.cache
	rp := renderParams{
		prims: c.prims, blockers: c.blockers, lights: c.lights,
		planeIdx: c.planeIdx, blockerPlaneIdx: c.blockerPlaneIdx,
		bvhNodes: c.bvhNodes, bvhNodeCount: c.bvhNodeCount, blockerNodeCount: c.blockerNodeCount,
		instTemplates: c.instTemplates, instPlacements: c.instPlacements,
		instNodeBase: c.instNodeBase, instNodeCount: c.instNodeCount,
		blockerSecStart: c.blockerSecStart, blockerInstBase: c.blockerInstBase,
		blockerInstCount: c.blockerInstCount,
	terrains: c.terrains, samples: c.samples,
		terrainFeatures: c.terrainFeatures, terrainPads: c.terrainPads,
		waters: c.waters,
		campfireParams: c.campfireParams, holes: c.holes, boxFaceTex: c.boxFaceTex, ao: c.ao, aoOK: c.aoOK && aoEnabled,
		shadows: shadows, mirror: mirror, timeSec: timeSec, sky: sky,
		bodyEnabled: bodyEnabled, bodyDir: bodyDir, bodyColor: bodyColor,
		bodyCosRadius: bodyCosRadius, bodyGlow: bodyGlow,
		uploadStatic: uploadStatic, uploadPartial: uploadPartial,
		partialPrimSpans:    c.partialPrimSpans,
		partialBlockerSpans: c.partialBlockerSpans,
	}
	if v != nil {
		rp.colorQuant = v.ColorQuant
		rp.maxBounceDepth = v.MaxBounceDepth
	}
	if v == nil || v.Scene == nil {
		rp = renderParams{}
	}
	if ver := texture.CaptureGPUVersion(); ver != r.captureVer {
		r.captureLoaded = false
		if w, h, px, ok := texture.PackCapturesGPU(); ok && len(px)*4 <= int(r.captureBytes) {
			r.captureW, r.captureH = w, h
			if err := r.queue.WriteBuffer(r.captures, 0, u32Bytes(px)); err == nil {
				r.captureLoaded = true
			}
		} else {
			r.captureW, r.captureH = 0, 0
		}
		r.captureVer = ver
	}
	if ver := texture.DocumentGPUVersion(); ver != r.documentVer || !r.documentLoaded {
		r.uploadDocumentTextures()
	}
	r.maybeProfileWorkload(&rp)
	return rp
}

// renderParams bundles one frame's packed scene buffers, keeping render's
// signature manageable as the GPU scene model grows.
type renderParams struct {
	prims, blockers  []GPUPrimitive
	planeIdx         []uint32
	blockerPlaneIdx  []uint32
	lights           []GPULight
	bvhNodes         []GPUBVHNode
	bvhNodeCount     uint32
	blockerNodeCount uint32
	instTemplates    []GPUTemplateRecord
	instPlacements   []GPUInstanceRecord
	instNodeBase     uint32
	instNodeCount    uint32
	blockerSecStart  uint32
	blockerInstBase  uint32
	blockerInstCount uint32
	terrains         []GPUTerrain
	samples          []float32
	terrainFeatures  []GPUTerrainFeature
	terrainPads      []GPUTerrainPad
	waters           []GPUWater
	campfireParams   []CampfireParams
	holes            []GPUHole
	boxFaceTex       []uint32
	ao               AOVolume
	aoOK             bool
	shadows          bool
	mirror           bool
	timeSec          float64
	sky              int
	// Visible celestial body (sun/moon disc) drawn in the sky. bodyDir points
	// from the camera toward the body (= -Env.SunDir); bodyCosRadius is the
	// cosine of its angular radius.
	bodyEnabled    bool
	bodyDir        vec.V
	bodyColor      vec.V
	bodyCosRadius  float32
	bodyGlow       float32
	colorQuant     uint32
	maxBounceDepth uint32
	profileEnabled bool
	// uploadStatic is set when the cached scene buffers changed this frame and
	// must be re-sent to the GPU. When false, render() uploads only the per-frame
	// params; the static SSBOs already hold the right data.
	uploadStatic bool
	// uploadPartial re-sends only dirty primitive spans + refit BVH after NPC pose updates.
	uploadPartial       bool
	partialPrimSpans    [][2]int
	partialBlockerSpans [][2]int
}

// uploadFrame writes the per-frame params and any dirty scene buffers to the
// GPU (via queue writes, which the next Submit consumes in order). It does not
// encode or submit any GPU work. It sets r.timing.Upload.
func (r *Renderer) uploadFrame(cam *camera.Camera, p renderParams, fw, fh int) error {
	uploadStart := time.Now()
	if p.profileEnabled {
		zeros := make([]byte, profileCounterBytes)
		if err := r.queue.WriteBuffer(r.profile, 0, zeros); err != nil {
			return err
		}
	}
	params := r.paramsBytes(cam, p, fw, fh)
	if err := r.queue.WriteBuffer(r.params, 0, params[:]); err != nil {
		return err
	}
	// Static scene buffers are re-sent only when the cache was rebuilt this
	// frame (scene swap or scene.Touch); otherwise the GPU already holds them.
	if p.uploadStatic {
		if len(p.prims) > 0 {
			if err := r.queue.WriteBuffer(r.prims, 0, primBytes(p.prims)); err != nil {
				return err
			}
		}
		if len(p.blockers) > 0 {
			if err := r.queue.WriteBuffer(r.blockers, 0, primBytes(p.blockers)); err != nil {
				return err
			}
		}
		if len(p.lights) > 0 {
			if err := r.queue.WriteBuffer(r.lights, 0, lightBytes(p.lights)); err != nil {
				return err
			}
		}
		if len(p.bvhNodes) > 0 {
			if err := r.queue.WriteBuffer(r.bvhNodes, 0, nodeBytes(p.bvhNodes)); err != nil {
				return err
			}
		}
		if len(p.terrains) > 0 {
			if err := r.queue.WriteBuffer(r.terrains, 0, terrainBytes(p.terrains)); err != nil {
				return err
			}
		}
		if len(p.samples) > 0 {
			if err := r.queue.WriteBuffer(r.samples, 0, floatBytes(p.samples)); err != nil {
				return err
			}
		}
		if len(p.terrainFeatures) > 0 {
			if err := r.queue.WriteBuffer(r.terrFeat, 0, terrainFeatureBytes(p.terrainFeatures)); err != nil {
				return err
			}
		}
		if len(p.terrainPads) > 0 {
			if err := r.queue.WriteBuffer(r.terrPads, 0, terrainPadBytes(p.terrainPads)); err != nil {
				return err
			}
		}
		if len(p.waters) > 0 {
			if err := r.queue.WriteBuffer(r.waters, 0, waterBytes(p.waters)); err != nil {
				return err
			}
		}
		if len(p.campfireParams) > 0 {
			if err := r.queue.WriteBuffer(r.campfires, 0, campfireBytes(p.campfireParams)); err != nil {
				return err
			}
		}
		if len(p.holes) > 0 {
			if err := r.queue.WriteBuffer(r.holes, 0, holeBytes(p.holes)); err != nil {
				return err
			}
		}
		if len(p.instTemplates) > 0 {
			if err := r.queue.WriteBuffer(r.instTmpl, 0, instTemplateBytes(p.instTemplates)); err != nil {
				return err
			}
		}
		if len(p.instPlacements) > 0 {
			if err := r.queue.WriteBuffer(r.instRecs, 0, instanceBytes(p.instPlacements)); err != nil {
				return err
			}
		}
		if len(p.boxFaceTex) > 0 {
			if err := r.queue.WriteBuffer(r.boxFaces, 0, u32Bytes(p.boxFaceTex)); err != nil {
				return err
			}
		}
		if len(p.planeIdx) > 0 {
			if err := r.queue.WriteBuffer(r.planeIdx, 0, u32Bytes(p.planeIdx)); err != nil {
				return err
			}
		}
		if len(p.blockerPlaneIdx) > 0 {
			if err := r.queue.WriteBuffer(r.blkPlane, 0, u32Bytes(p.blockerPlaneIdx)); err != nil {
				return err
			}
		}
		// Upload whenever the cache holds an AO volume, independent of the runtime
		// AO toggle: the toggle only gates the shader's sampling (the aoOK uniform
		// in paramsBytes), so flipping it on later needs no re-pack/re-upload.
		if len(p.ao.Data) > 0 {
			if err := r.queue.WriteBuffer(r.aoVolume, 0, floatBytes(p.ao.Data)); err != nil {
				return err
			}
		}
	} else if p.uploadPartial {
		for _, span := range p.partialPrimSpans {
			if span[0] < 0 || span[1] > len(p.prims) || span[0] >= span[1] {
				continue
			}
			offset := uint64(span[0] * primStride)
			slice := p.prims[span[0]:span[1]]
			if err := r.queue.WriteBuffer(r.prims, offset, primBytes(slice)); err != nil {
				return err
			}
		}
		for _, span := range p.partialBlockerSpans {
			if span[0] < 0 || span[1] > len(p.blockers) || span[0] >= span[1] {
				continue
			}
			offset := uint64(span[0] * primStride)
			slice := p.blockers[span[0]:span[1]]
			if err := r.queue.WriteBuffer(r.blockers, offset, primBytes(slice)); err != nil {
				return err
			}
		}
		if len(p.bvhNodes) > 0 && p.bvhNodeCount > 0 {
			nodes := p.bvhNodes[:p.bvhNodeCount]
			if err := r.queue.WriteBuffer(r.bvhNodes, 0, nodeBytes(nodes)); err != nil {
				return err
			}
		}
		if len(p.bvhNodes) > 0 && p.blockerNodeCount > 0 {
			start := int(p.blockerSecStart)
			end := start + int(p.blockerNodeCount)
			if start >= 0 && end <= len(p.bvhNodes) {
				nodes := p.bvhNodes[start:end]
				offset := uint64(start * nodeStride)
				if err := r.queue.WriteBuffer(r.bvhNodes, offset, nodeBytes(nodes)); err != nil {
					return err
				}
			}
		}
	}
	r.timing.Upload = time.Since(uploadStart)
	return nil
}

// submitTrace encodes and submits one compute dispatch, copying the rendered
// output (and, when profiling, the atomic counters) into dst. It does not wait
// on the GPU; the returned submission index lets the caller poll for it later.
func (r *Renderer) submitTrace(dst *wgpu.Buffer, fw, fh int, profiled bool) (wgpu.SubmissionIndex, error) {
	encoder, err := r.device.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "trace encoder"})
	if err != nil {
		return 0, err
	}
	defer encoder.Release()

	pass := encoder.BeginComputePass(&wgpu.ComputePassDescriptor{Label: "trace pass"})
	pass.SetPipeline(r.pipeline)
	pass.SetBindGroup(0, r.bind, nil)
	pass.DispatchWorkgroups(uint32((fw+workgroupXY-1)/workgroupXY), uint32((fh+workgroupXY-1)/workgroupXY), 1)
	if err := pass.End(); err != nil {
		pass.Release()
		return 0, err
	}
	pass.Release()

	size := uint64(fw * fh * 4)
	if profiled {
		if err := encoder.CopyBufferToBuffer(r.profile, 0, r.profileRead, 0, profileCounterBytes); err != nil {
			return 0, err
		}
	}
	if err := encoder.CopyBufferToBuffer(r.output, 0, dst, 0, size); err != nil {
		return 0, err
	}
	cmd, err := encoder.Finish(&wgpu.CommandBufferDescriptor{Label: "trace command buffer"})
	if err != nil {
		return 0, err
	}
	defer cmd.Release()
	return r.queue.Submit(cmd), nil
}

// mapReadInto maps b (already rendered) and copies size bytes into buf. It polls
// the whole device to service the map callback, so it must only be used once the
// relevant submission is known complete (the synchronous path).
func (r *Renderer) mapReadInto(b *wgpu.Buffer, size uint64, buf []byte) error {
	done := make(chan error, 1)
	if err := b.MapAsync(wgpu.MapMode_Read, 0, size, func(status wgpu.BufferMapAsyncStatus) {
		if status != wgpu.BufferMapAsyncStatus_Success {
			done <- fmt.Errorf("map readback: %s", status)
			return
		}
		done <- nil
	}); err != nil {
		return err
	}
	r.device.Poll(true, nil)
	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-time.After(2 * time.Second):
		return fmt.Errorf("map readback timeout")
	}
	copy(buf, b.GetMappedRange(0, uint(size)))
	return b.Unmap()
}

// mapReadOnSub maps b and copies size bytes into buf, waiting only for the given
// submission (not the entire device queue). The pipelined path uses this to read
// back the *previous* frame without blocking on the frame it just submitted.
func (r *Renderer) mapReadOnSub(b *wgpu.Buffer, size uint64, sub wgpu.SubmissionIndex, buf []byte) error {
	done := make(chan error, 1)
	if err := b.MapAsync(wgpu.MapMode_Read, 0, size, func(status wgpu.BufferMapAsyncStatus) {
		if status != wgpu.BufferMapAsyncStatus_Success {
			done <- fmt.Errorf("map readback: %s", status)
			return
		}
		done <- nil
	}); err != nil {
		return err
	}
	wrapped := wgpu.WrappedSubmissionIndex{Queue: r.queue, SubmissionIndex: sub}
	r.device.Poll(true, &wrapped)
	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-time.After(2 * time.Second):
		return fmt.Errorf("map readback timeout")
	}
	copy(buf, b.GetMappedRange(0, uint(size)))
	return b.Unmap()
}

// render is the synchronous path: submit one frame and block until its pixels
// are read back. Used by RenderSquare (portal captures), the profiling
// benchmark, and whenever pipelining is disabled.
func (r *Renderer) render(buf []byte, cam *camera.Camera, p renderParams, fw, fh int) error {
	frameStart := time.Now()
	if err := r.uploadFrame(cam, p, fw, fh); err != nil {
		return err
	}
	gpuStart := time.Now()
	sub, err := r.submitTrace(r.read, fw, fh, p.profileEnabled)
	if err != nil {
		return err
	}
	wrapped := wgpu.WrappedSubmissionIndex{Queue: r.queue, SubmissionIndex: sub}
	r.device.Poll(true, &wrapped)
	r.timing.GPU = time.Since(gpuStart)
	// This is a real submit->idle measurement; remember it as the true GPU cost
	// for the pipelined frames (which never block on the GPU) to report.
	r.gpuTime = r.timing.GPU

	readStart := time.Now()
	size := uint64(fw * fh * 4)
	if err := r.mapReadInto(r.read, size, buf); err != nil {
		return err
	}
	if p.profileEnabled {
		if err := r.readProfileCounters(); err != nil {
			return err
		}
	}
	r.timing.Readback = time.Since(readStart)
	r.timing.Total = time.Since(frameStart)
	return nil
}

// renderPipelined submits the current frame without waiting on it, then hands
// back the previous frame's pixels. The GPU renders frame N while the CPU packs
// and blits, instead of the whole thread stalling on device.Poll every frame.
// The cost is one frame of latency (the first frame returns black). Only the
// full-resolution main path (r.w × r.h) uses this.
func (r *Renderer) renderPipelined(buf []byte, cam *camera.Camera, p renderParams) error {
	frameStart := time.Now()
	if err := r.uploadFrame(cam, p, r.w, r.h); err != nil {
		return err
	}
	size := uint64(r.w * r.h * 4)
	curSlot := r.pipeParity

	sub, err := r.submitTrace(r.reads[curSlot], r.w, r.h, p.profileEnabled)
	if err != nil {
		return err
	}
	// The submit itself does not block, so the async "GPU time" would be ~0 and
	// meaningless. Report the last true GPU cost measured on a sync sample frame.
	r.timing.GPU = r.gpuTime

	readStart := time.Now()
	if r.pipePending {
		if err := r.mapReadOnSub(r.reads[r.pipeSlot], r.pipeSize, r.pipeSub, buf); err != nil {
			return err
		}
		if r.pipeProfiled {
			if err := r.readProfileCounters(); err != nil {
				return err
			}
		}
	} else if r.lastFrameReady {
		// No previous frame in flight (startup, or just after a sync sample
		// frame). Reuse the last presented pixels so nothing flashes black.
		copy(buf, r.lastFrame)
	} else {
		for i := range buf {
			buf[i] = 0
		}
	}
	r.timing.Readback = time.Since(readStart)

	r.pipePending = true
	r.pipeSlot = curSlot
	r.pipeSub = sub
	r.pipeSize = size
	r.pipeProfiled = p.profileEnabled
	r.pipeParity ^= 1
	r.timing.Total = time.Since(frameStart)
	return nil
}

// discardPending drains any in-flight pipelined frame, freeing its staging
// buffer. Used before falling back to the synchronous path (e.g. profiling).
func (r *Renderer) discardPending() {
	if !r.pipePending {
		return
	}
	tmp := make([]byte, r.pipeSize)
	_ = r.mapReadOnSub(r.reads[r.pipeSlot], r.pipeSize, r.pipeSub, tmp)
	r.pipePending = false
}

// SetPipelined enables one-frame-deep CPU/GPU pipelining for the main Render
// path. Off by default so single-shot callers (tests, preview, gpuprof) and
// portal captures keep exact synchronous, same-frame semantics.
func (r *Renderer) SetPipelined(on bool) { r.pipelined = on }

func (r *Renderer) paramsBytes(cam *camera.Camera, p renderParams, fw, fh int) [paramsSize]byte {
	fwd, right, up := cam.Basis()
	var out [paramsSize]byte
	putU32(out[0:4], uint32(fw))
	putU32(out[4:8], uint32(fh))
	putU32(out[8:12], uint32(len(p.prims)))
	putU32(out[12:16], uint32(len(p.lights)))
	putU32(out[16:20], uint32(len(p.blockers)))
	if p.shadows {
		putU32(out[20:24], 1)
	}
	putU32(out[24:28], p.bvhNodeCount)
	putU32(out[28:32], p.blockerNodeCount)
	putU32(out[32:36], uint32(len(p.terrains)))
	putU32(out[36:40], uint32(len(p.waters)))
	putF32(out[40:44], float32(p.timeSec))
	putU32(out[44:48], p.maxBounceDepth)
	putF32(out[48:52], float32(float64(fw)/float64(fh)))
	putF32(out[52:56], float32(fovScale))
	putF32(out[56:60], float32(ambientFlat))
	if p.mirror {
		putU32(out[60:64], 1)
	}
	putVec4(out[64:80], cam.Pos)
	putVec4(out[80:96], fwd)
	putVec4(out[96:112], right)
	putVec4(out[112:128], up)
	// Campfire + ambient-occlusion volume params.
	putU32(out[128:132], uint32(len(p.campfireParams)))
	if p.aoOK {
		putU32(out[132:136], 1)
		putU32(out[136:140], uint32(p.ao.NX))
		putU32(out[140:144], uint32(p.ao.NY))
		putU32(out[144:148], uint32(p.ao.NZ))
		putF32(out[148:152], float32(p.ao.Inv))
		putF32(out[152:156], float32(p.ao.Cell))
		putF32(out[156:160], float32(p.ao.Bias))
		putVec4(out[160:176], p.ao.Min)
	}
	putU32(out[176:180], uint32(p.sky))
	// Celestial body (sun/moon disc): enable flag + disc geometry, then the
	// direction and color vec4s (16-byte aligned at 192/208). See the Params
	// struct in trace.wgsl for the matching layout.
	if p.bodyEnabled {
		putU32(out[180:184], 1)
	}
	putF32(out[184:188], p.bodyCosRadius)
	putF32(out[188:192], p.bodyGlow)
	putVec4(out[192:208], p.bodyDir)
	putVec4(out[208:224], p.bodyColor)
	putU32(out[224:228], p.colorQuant)
	if r.captureLoaded {
		putU32(out[228:232], 1)
		putU32(out[232:236], uint32(r.captureW))
		putU32(out[236:240], uint32(r.captureH))
	}
	if r.documentLoaded {
		putU32(out[240:244], 1)
	}
	putU32(out[244:248], uint32(len(p.instTemplates)))
	putU32(out[248:252], uint32(len(p.instPlacements)))
	putU32(out[252:256], p.instNodeBase)
	putU32(out[256:260], p.instNodeCount)
	putU32(out[260:264], p.blockerSecStart)
	putU32(out[264:268], p.blockerInstBase)
	putU32(out[268:272], p.blockerInstCount)
	if p.profileEnabled {
		putU32(out[272:276], 1)
	}
	putU32(out[276:280], uint32(len(p.planeIdx)))
	putU32(out[280:284], uint32(len(p.blockerPlaneIdx)))
	return out
}

func (r *Renderer) readProfileCounters() error {
	done := make(chan error, 1)
	if err := r.profileRead.MapAsync(wgpu.MapMode_Read, 0, profileCounterBytes, func(status wgpu.BufferMapAsyncStatus) {
		if status != wgpu.BufferMapAsyncStatus_Success {
			done <- fmt.Errorf("map profile readback: %s", status)
			return
		}
		done <- nil
	}); err != nil {
		return err
	}
	r.device.Poll(true, nil)
	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-time.After(2 * time.Second):
		return fmt.Errorf("map profile readback timeout")
	}
	raw := r.profileRead.GetMappedRange(0, profileCounterBytes)
	r.profileCounters = decodeProfileCounters(raw)
	if err := r.profileRead.Unmap(); err != nil {
		return err
	}
	if !r.profiling {
		r.absorbWorkloadSample(r.profileCounters)
	}
	return nil
}

func putU32(dst []byte, v uint32) { binary.LittleEndian.PutUint32(dst, v) }

func putF32(dst []byte, v float32) { binary.LittleEndian.PutUint32(dst, math.Float32bits(v)) }

func putVec4(dst []byte, v vec.V) {
	putF32(dst[0:4], float32(v.X))
	putF32(dst[4:8], float32(v.Y))
	putF32(dst[8:12], float32(v.Z))
	putF32(dst[12:16], 0)
}

// Release frees WebGPU resources. The app currently lives for process lifetime,
// but tests and later backend switching can call this explicitly.
func (r *Renderer) Release() {
	if r == nil {
		return
	}
	if r.bind != nil {
		r.bind.Release()
	}
	if r.pipeline != nil {
		r.pipeline.Release()
	}
	if r.profileRead != nil {
		r.profileRead.Release()
	}
	if r.profile != nil {
		r.profile.Release()
	}
	if r.read != nil {
		r.read.Release()
	}
	for i := range r.reads {
		if r.reads[i] != nil {
			r.reads[i].Release()
		}
	}
	if r.planeIdx != nil {
		r.planeIdx.Release()
	}
	if r.blkPlane != nil {
		r.blkPlane.Release()
	}
	if r.output != nil {
		r.output.Release()
	}
	if r.holes != nil {
		r.holes.Release()
	}
	if r.instTmpl != nil {
		r.instTmpl.Release()
	}
	if r.instRecs != nil {
		r.instRecs.Release()
	}
	if r.captures != nil {
		r.captures.Release()
	}
	if r.documents != nil {
		r.documents.Release()
	}
	if r.boxFaces != nil {
		r.boxFaces.Release()
	}
	if r.campfires != nil {
		r.campfires.Release()
	}
	if r.aoVolume != nil {
		r.aoVolume.Release()
	}
	if r.perm != nil {
		r.perm.Release()
	}
	if r.waters != nil {
		r.waters.Release()
	}
	if r.samples != nil {
		r.samples.Release()
	}
	if r.terrFeat != nil {
		r.terrFeat.Release()
	}
	if r.terrPads != nil {
		r.terrPads.Release()
	}
	if r.terrains != nil {
		r.terrains.Release()
	}
	if r.bvhNodes != nil {
		r.bvhNodes.Release()
	}
	if r.lights != nil {
		r.lights.Release()
	}
	if r.blockers != nil {
		r.blockers.Release()
	}
	if r.prims != nil {
		r.prims.Release()
	}
	if r.params != nil {
		r.params.Release()
	}
	if r.queue != nil {
		r.queue.Release()
	}
	if r.device != nil {
		r.device.Release()
	}
	if r.adapter != nil {
		r.adapter.Release()
	}
	if r.instance != nil {
		r.instance.Release()
	}
}
