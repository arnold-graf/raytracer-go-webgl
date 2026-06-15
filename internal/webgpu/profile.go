package webgpu

import (
	"raytracer/internal/render"
	"time"
)

// FrameTiming breaks one Render call into its sequential phases so the GPU
// backend can be profiled without an external GPU debugger.
//
// Pack and Upload are CPU work the backend currently repeats every frame (the
// scene is re-packed into GPU layouts and re-uploaded each Render, even when it
// is static). GPU is the submit->complete wall time of the compute dispatch,
// measured by blocking on device.Poll(true). Readback is the cost of mapping
// the output storage buffer and copying it back into the framebuffer.
//
// Because Render blocks until the GPU is idle, GPU here is a faithful proxy for
// shader cost at the scene's current camera; it is not overlapped with the next
// frame's CPU work the way a presented swapchain would be.
type FrameTiming struct {
	Pack     time.Duration
	Upload   time.Duration
	GPU      time.Duration
	Readback time.Duration
	Total    time.Duration

	// Scene size that produced this frame (post-cap), handy for attributing
	// pack/GPU cost to geometry growth.
	Prims    int
	Blockers int
	BVHNodes int
	Holes    int
}

// LastTiming returns the phase breakdown of the most recent Render call. It is
// refreshed every frame at negligible cost (a handful of time.Now calls) so the
// app HUD and the cmd/gpuprof benchmark can read it without a profiling build.
func (r *Renderer) LastTiming() FrameTiming { return r.timing }

// LastPhaseTimings implements render.PhaseTimingsProvider for the in-game HUD.
func (r *Renderer) LastPhaseTimings() render.PhaseTimings {
	t := r.timing
	return render.PhaseTimings{
		Pack:     durMs(t.Pack),
		Upload:   durMs(t.Upload),
		GPU:      durMs(t.GPU),
		Readback: durMs(t.Readback),
		Total:    durMs(t.Total),
		Prims:    t.Prims,
		Blockers: t.Blockers,
		BVHNodes: t.BVHNodes,
		Holes:    t.Holes,
	}
}

func durMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// BackendName implements render.BackendNamer so the HUD can label the active
// backend.
func (r *Renderer) BackendName() string { return "webgpu" }
