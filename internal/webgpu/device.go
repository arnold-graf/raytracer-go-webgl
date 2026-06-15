// Package webgpu contains the WebGPU renderer backend, the only renderer in the
// app. It packs the scene into GPU buffers and runs the path tracer as a compute
// shader (shaders/trace.wgsl).
package webgpu

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/vec"

	"github.com/rajveermalviya/go-webgpu/wgpu"
)

//go:embed shaders/trace.wgsl
var traceWGSL string

const (
	fovScale    = 0.5773502691896257 // tan(60deg / 2)
	paramsSize  = 256
	workgroupXY = 8

	// ambientFlat is the CPU shade()'s flat-ambient term used when a scene has
	// no hemispheric sky/ground ambient: lit = albedo * 0.04.
	ambientFlat = 0.04
)

// Renderer is the early WebGPU backend: it dispatches a compute shader into a
// storage buffer, reads that buffer back, and lets the existing Ebiten app blit
// it. This readback path is deliberately temporary; the real WebGPU renderer
// will present directly to a surface once parity is useful.
type Renderer struct {
	w, h int

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
	waters    *wgpu.Buffer
	perm      *wgpu.Buffer
	aoVolume  *wgpu.Buffer
	campfires *wgpu.Buffer
	holes     *wgpu.Buffer
	output    *wgpu.Buffer
	read      *wgpu.Buffer
	pipeline  *wgpu.ComputePipeline
	bind      *wgpu.BindGroup

	cache  sceneCache  // memoized static scene buffers (see cache.go)
	timing FrameTiming // phase breakdown of the most recent Render (see profile.go)
}

// New initializes WebGPU and compiles the skeleton sky compute pipeline.
func New(w, h int) (*Renderer, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid render size %dx%d", w, h)
	}
	r := &Renderer{w: w, h: h}
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

	size := uint64(r.w * r.h * 4)
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
		Size:  maxBVHNodes * 2 * nodeStride,
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

	shader, err := r.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label:          "trace shader",
		WGSLDescriptor: &wgpu.ShaderModuleWGSLDescriptor{Code: traceWGSL},
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
			{Binding: 5, Buffer: r.bvhNodes, Size: maxBVHNodes * 2 * nodeStride},
			{Binding: 6, Buffer: r.terrains, Size: maxTerrains * terrainStride},
			{Binding: 7, Buffer: r.samples, Size: maxTerrainVals * 16},
			{Binding: 8, Buffer: r.waters, Size: maxWaters * waterStride},
			{Binding: 9, Buffer: r.perm, Size: permCount * 4},
			{Binding: 10, Buffer: r.aoVolume, Size: maxAOFloats * 4},
			{Binding: 11, Buffer: r.campfires, Size: maxCampfires * campfireStride},
			{Binding: 12, Buffer: r.holes, Size: maxHoles * holeStride},
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
	var campfires []GPUCampfire
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
	// Static buffers (geometry, BVH, terrain, lights, holes, AO) are packed only
	// when the scene changes; uploadStatic tells render() whether they need to be
	// re-sent to the GPU this frame. Campfires (time-animated flicker) and params
	// (camera + clock) stay per-frame.
	uploadStatic := false
	packStart := time.Now()
	if v != nil && v.Scene != nil {
		if !r.cache.fresh(v) {
			r.cache.rebuild(v)
			uploadStatic = true
		}
		campfires = PackCampfires(v.Scene, v.Time)
		timeSec = v.Time
		shadows = v.Shadow
		mirror = v.Mirror
		aoEnabled = v.AO
		sky = v.Scene.Env.Sky
		if env := v.Scene.Env; env.Sun.Visible() && env.SunDir != (vec.V{}) {
			bodyEnabled = true
			bodyDir = env.SunDir.Scale(-1).Normalize() // body sits where the light comes from
			bodyColor = env.Sun.Color
			radius := env.Sun.Size * 0.5 * math.Pi / 180.0 // diameter (deg) -> radius (rad)
			bodyCosRadius = float32(math.Cos(radius))
			bodyGlow = float32(env.Sun.Glow)
		}
	}
	c := &r.cache
	rp := renderParams{
		prims: c.prims, blockers: c.blockers, lights: c.lights,
		bvhNodes: c.bvhNodes, bvhNodeCount: c.bvhNodeCount, blockerNodeCount: c.blockerNodeCount,
		terrains: c.terrains, samples: c.samples, waters: c.waters,
		campfires: campfires, holes: c.holes, ao: c.ao, aoOK: c.aoOK && aoEnabled,
		shadows: shadows, mirror: mirror, timeSec: timeSec, sky: sky,
		bodyEnabled: bodyEnabled, bodyDir: bodyDir, bodyColor: bodyColor,
		bodyCosRadius: bodyCosRadius, bodyGlow: bodyGlow,
		uploadStatic: uploadStatic,
	}
	if v == nil || v.Scene == nil {
		rp = renderParams{}
	}
	r.timing = FrameTiming{
		Pack:     time.Since(packStart),
		Prims:    len(rp.prims),
		Blockers: len(rp.blockers),
		BVHNodes: len(rp.bvhNodes),
		Holes:    len(rp.holes),
	}
	if err := r.render(buf[:r.w*r.h*4], cam, rp); err != nil {
		// Keep the app alive if WebGPU has a transient validation/device issue.
		// Fill magenta so a broken backend is unmistakable.
		for i := 0; i < r.w*r.h; i++ {
			o := i * 4
			buf[o], buf[o+1], buf[o+2], buf[o+3] = 255, 0, 255, 255
		}
	}
}

// renderParams bundles one frame's packed scene buffers, keeping render's
// signature manageable as the GPU scene model grows.
type renderParams struct {
	prims, blockers  []GPUPrimitive
	lights           []GPULight
	bvhNodes         []GPUBVHNode
	bvhNodeCount     uint32
	blockerNodeCount uint32
	terrains         []GPUTerrain
	samples          []float32
	waters           []GPUWater
	campfires        []GPUCampfire
	holes            []GPUHole
	ao               AOVolume
	aoOK             bool
	shadows          bool
	mirror           bool
	timeSec          float64
	sky              int
	// Visible celestial body (sun/moon disc) drawn in the sky. bodyDir points
	// from the camera toward the body (= -Env.SunDir); bodyCosRadius is the
	// cosine of its angular radius.
	bodyEnabled   bool
	bodyDir       vec.V
	bodyColor     vec.V
	bodyCosRadius float32
	bodyGlow      float32
	// uploadStatic is set when the cached scene buffers changed this frame and
	// must be re-sent to the GPU. When false, render() uploads only the per-frame
	// params and campfires; the static SSBOs already hold the right data.
	uploadStatic bool
}

func (r *Renderer) render(buf []byte, cam *camera.Camera, p renderParams) error {
	frameStart := time.Now()
	uploadStart := time.Now()
	params := r.paramsBytes(cam, p)
	if err := r.queue.WriteBuffer(r.params, 0, params[:]); err != nil {
		return err
	}
	// Campfires animate (flicker) every frame, so they always re-upload.
	if len(p.campfires) > 0 {
		if err := r.queue.WriteBuffer(r.campfires, 0, campfireBytes(p.campfires)); err != nil {
			return err
		}
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
		if len(p.waters) > 0 {
			if err := r.queue.WriteBuffer(r.waters, 0, waterBytes(p.waters)); err != nil {
				return err
			}
		}
		if len(p.holes) > 0 {
			if err := r.queue.WriteBuffer(r.holes, 0, holeBytes(p.holes)); err != nil {
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
	}
	r.timing.Upload = time.Since(uploadStart)

	gpuStart := time.Now()
	encoder, err := r.device.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "trace encoder"})
	if err != nil {
		return err
	}
	defer encoder.Release()

	pass := encoder.BeginComputePass(&wgpu.ComputePassDescriptor{Label: "trace pass"})
	pass.SetPipeline(r.pipeline)
	pass.SetBindGroup(0, r.bind, nil)
	pass.DispatchWorkgroups(uint32((r.w+workgroupXY-1)/workgroupXY), uint32((r.h+workgroupXY-1)/workgroupXY), 1)
	if err := pass.End(); err != nil {
		pass.Release()
		return err
	}
	pass.Release()

	size := uint64(r.w * r.h * 4)
	if err := encoder.CopyBufferToBuffer(r.output, 0, r.read, 0, size); err != nil {
		return err
	}
	cmd, err := encoder.Finish(&wgpu.CommandBufferDescriptor{Label: "trace command buffer"})
	if err != nil {
		return err
	}
	defer cmd.Release()

	submission := r.queue.Submit(cmd)
	wrapped := wgpu.WrappedSubmissionIndex{Queue: r.queue, SubmissionIndex: submission}
	r.device.Poll(true, &wrapped)
	r.timing.GPU = time.Since(gpuStart)

	readStart := time.Now()
	done := make(chan error, 1)
	if err := r.read.MapAsync(wgpu.MapMode_Read, 0, size, func(status wgpu.BufferMapAsyncStatus) {
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
	copy(buf, r.read.GetMappedRange(0, uint(size)))
	if err := r.read.Unmap(); err != nil {
		return err
	}
	r.timing.Readback = time.Since(readStart)
	r.timing.Total = time.Since(frameStart)
	return nil
}

func (r *Renderer) paramsBytes(cam *camera.Camera, p renderParams) [paramsSize]byte {
	fwd, right, up := cam.Basis()
	var out [paramsSize]byte
	putU32(out[0:4], uint32(r.w))
	putU32(out[4:8], uint32(r.h))
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
	// out[44:48] padding
	putF32(out[48:52], float32(float64(r.w)/float64(r.h)))
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
	putU32(out[128:132], uint32(len(p.campfires)))
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
	return out
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
	if r.read != nil {
		r.read.Release()
	}
	if r.output != nil {
		r.output.Release()
	}
	if r.holes != nil {
		r.holes.Release()
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
