package webgpu

import (
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"sync"

	"github.com/rajveermalviya/go-webgpu/wgpu"

	"raytracer/internal/camera"
	"raytracer/internal/render"
)

// UpdateProbeField runs one iteration of the path-traced probe update.
//
// The same field the megakernel's own probe pass fills, and the same storage —
// gi.wesl owns the octahedral layout, the cascades and the sparse baked volume
// for both. What differs is only the transport: this follows real paths to
// ptp.max_depth, where the megakernel traces one bounce and takes the rest
// from the previous frame's field.
//
// blend is the rate each texel moves toward this iteration's estimate. At
// 1/(k+1) on the k'th call it is an exact running mean, which is what a bake
// wants; a live near field wants a small fixed rate instead.
//
// probes is how many to refresh. Zero means "the whole loaded bake".
func (pt *PathTracer) UpdateProbeField(cam *camera.Camera, v *render.View, blend float32, probes uint32) error {
	return pt.updateProbeField(cam, v, blend, probes, false)
}

// UpdateProbeFieldSync is UpdateProbeField, waiting for the GPU before it
// returns. The baker needs it and a live frame does not.
func (pt *PathTracer) UpdateProbeFieldSync(cam *camera.Camera, v *render.View, blend float32, probes uint32) error {
	return pt.updateProbeField(cam, v, blend, probes, true)
}

func (pt *PathTracer) updateProbeField(cam *camera.Camera, v *render.View, blend float32, probes uint32, sync bool) error {
	r := pt.r
	if r.baked == nil && !liveGIEnabled() {
		return fmt.Errorf("probe field is off; set RAYTRACER_LIVE_GI or RAYTRACER_GI_BAKE")
	}
	// From here the megakernel's own probe pass stands down: this is the
	// transport now.
	r.giExternal = true
	p := r.buildRenderParams(v)
	// The probe pass reads its schedule out of the shared uniform block, so
	// the megakernel's params are what drive it — the path tracer supplies
	// transport and nothing else.
	p.bakeBlend = blend
	if probes > 0 && r.baked != nil {
		// A bake supplies the far field, so the whole budget goes to the fine
		// cascade — which is the point of running live at all.
		fine := giFine().probes()
		p.probeCount = min32(probes, fine)
		p.probeCount2 = 0
		p.probeBase = uint32((uint64(r.giFrame) * uint64(max32(p.probeCount, 1))) % uint64(max32(fine, 1)))
		p.probeSeed = uint32(r.giFrame)
		p.giNearLive = 1
		r.giFrame++
	} else if probes > 0 {
		// Split the budget across both cascades and walk each a slice at a
		// time, as the megakernel's pass does, so every probe comes round
		// rather than the same slice refreshing forever.
		//
		// The coarse cascade is not optional. Filling only the fine one leaves
		// everything past its 32 m box unlit, which is not a neutral failure:
		// it reads as the world going black a short walk away.
		fine := giFine().probes()
		coarse := p.gi.cells()
		nf := min32((probes+1)/2, fine)
		nc := min32(probes-nf, coarse)
		p.probeCount = nf
		p.probeBase = uint32((uint64(r.giFrame) * uint64(max32(nf, 1))) % uint64(max32(fine, 1)))
		p.probeCount2 = nc
		p.probeBase2 = uint32((uint64(r.giFrame) * uint64(max32(nc, 1))) % uint64(max32(coarse, 1)))
		p.probeSeed = uint32(r.giFrame)
		r.giFrame++
	}
	if p.probeCount == 0 && p.bakeProbes > 0 {
		p.probeCount = p.bakeProbes
	}
	groups := (p.probeCount + p.probeCount2 + giProbesPerWorkgroup - 1) / giProbesPerWorkgroup
	if groups == 0 {
		return nil
	}
	w := min32(groups, probeDispatchWidth)
	p.probeDispatchW = w
	if err := r.uploadFrame(cam, p, r.w, r.h); err != nil {
		return err
	}
	if err := pt.syncEmitters(p); err != nil {
		return err
	}
	if err := pt.writeSlots(1); err != nil {
		return err
	}

	enc, err := r.device.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "pt probe encoder"})
	if err != nil {
		return err
	}
	defer enc.Release()
	pass := enc.BeginComputePass(&wgpu.ComputePassDescriptor{Label: "pt probe pass"})
	pass.SetPipeline(pt.probePipe)
	pass.SetBindGroup(0, pt.bind, []uint32{slotOffset(0)})
	if os.Getenv("RTDBG") != "" {
		log.Printf("probe dispatch: probes=%d groups=%d w=%d h=%d dispatchW=%d",
			p.probeCount+p.probeCount2, groups, w, (groups+w-1)/w, p.probeDispatchW)
	}
	pass.DispatchWorkgroups(w, (groups+w-1)/w, 1)
	if err := pass.End(); err != nil {
		pass.Release()
		return err
	}
	pass.Release()

	cmd, err := enc.Finish(nil)
	if err != nil {
		return err
	}
	// Submitted and left to run. Nothing here reads the result back, and the
	// megakernel's own frame is submitted to the same queue afterwards, so
	// ordering already guarantees the field is written before it is sampled.
	// Blocking on it instead cost 3.6 ms a frame — far more than the rays.
	sub := r.queue.Submit(cmd)
	cmd.Release()
	if sync {
		wrapped := wgpu.WrappedSubmissionIndex{Queue: r.queue, SubmissionIndex: sub}
		r.device.Poll(true, &wrapped)
	}
	return nil
}

// livePTSettings reads the live near-field path tracer's configuration.
//
// An env var rather than a method, so it needs no plumbing through the app:
// internal/app holds the renderer behind an interface and knows nothing about
// path tracers. RAYTRACER_LIVE_GI_PT is how many probes to trace a frame;
// RAYTRACER_LIVE_GI_PT_DEPTH is the path length, 2 by default.
func livePTSettings() (probes uint32, depth uint32) {
	livePTOverride.once.Do(func() {
		n, err := strconv.Atoi(os.Getenv("RAYTRACER_LIVE_GI_PT"))
		if err != nil || n <= 0 {
			return
		}
		livePTOverride.probes = uint32(n)
		livePTOverride.depth = 2
		if d, err := strconv.Atoi(os.Getenv("RAYTRACER_LIVE_GI_PT_DEPTH")); err == nil && d > 0 {
			livePTOverride.depth = uint32(d)
		}
	})
	return livePTOverride.probes, livePTOverride.depth
}

var livePTOverride struct {
	once   sync.Once
	probes uint32
	depth  uint32
}

// driveLiveGI refreshes a slice of the near field before the frame that reads
// it, when RAYTRACER_LIVE_GI_PT asks for it.
//
// The path tracer is built on first use: it allocates a scratch arena sized to
// the render target, which is not worth paying for unless something asks.
// Failing to build it is reported once and then left alone — a renderer that
// cannot path trace should still draw.
func (r *Renderer) driveLiveGI(cam *camera.Camera, v *render.View) {
	probes, depth := livePTSettings()
	if probes == 0 || v == nil || v.Scene == nil || r.livePTFailed {
		return
	}
	if r.livePT == nil {
		pt, err := NewPathTracer(r, PTOptions{
			MaxDepth: depth, RRDepth: 3, LightScale: math.Pi,
			ClampIndirect: 12, LightSamples: 1, RISCandidates: 8,
		})
		if err != nil {
			log.Printf("live GI path tracer unavailable, falling back to the megakernel's probe pass: %v", err)
			r.livePTFailed = true
			return
		}
		r.livePT = pt
	}
	// A small fixed rate: a live field has to follow the lighting, not average
	// all of history the way a bake does.
	if err := r.livePT.UpdateProbeField(cam, v, liveGIBlend, probes); err != nil {
		log.Printf("live GI probe update failed, falling back: %v", err)
		r.livePTFailed = true
	}
}

// liveGIBlend is how far a probe moves toward each update.
const liveGIBlend = 0.05
