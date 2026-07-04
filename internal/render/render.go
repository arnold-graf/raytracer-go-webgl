// Package render defines the backend contract the app draws through. The only
// implementation is the WebGPU renderer (internal/webgpu); this package holds
// the renderer interface plus the small, backend-agnostic types it exchanges
// with the app (the per-frame View, profiling timings, and the backend name).
package render

import (
	"fmt"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/scene"
)

// View is one frame's worth of scene state handed to the renderer: the scene to
// draw, the animation clock, the feature toggles the GPU honors per frame, and
// the baked ambient-occlusion volume (computed once on the CPU and uploaded by
// the backend). It carries no camera; that is passed separately so the same
// View can be reused while the camera moves.
type View struct {
	Scene  *scene.Scene
	Time   float64 // animation clock in seconds (e.g. water ripples)
	Shadow bool    // cast shadow rays
	Mirror bool    // trace mirror/glass reflections
	AO     bool    // apply the baked ambient-occlusion volume

	// AOData is the baked ambient-occlusion volume; AOok is false when the scene
	// has no finite geometry to occlude against. The backend uploads it only
	// when both AOok and the AO toggle are set.
	AOData    probe.AOData
	AOok      bool
	AOVersion uint64 // bumps when AOData is replaced (e.g. after an async bake)

	// ColorQuant selects the post-dither color depth: 0 = 8-bit, 1 = 15-bit, 2 = 256 (3-3-2).
	ColorQuant uint32

	// MaxBounceDepth caps mirror/glass/water recursion in the tracer (default 2 when 0).
	// Raised while the spyglass is up so rays can pass through its lenses and scene glass.
	MaxBounceDepth uint32
}

// Renderer is the drawing backend the app depends on. Render fills buf
// (len = W*H*4, RGBA) by rendering v from cam. pixSize is a quality/speed knob
// (1 = full resolution) that backends may honor or ignore.
type Renderer interface {
	Render(buf []byte, cam *camera.Camera, v *View, pixSize int)
}

// SquareCapturer is optionally implemented by a renderer that can render a
// square (1:1 aspect) frame for portal capture textures.
type SquareCapturer interface {
	RenderSquare(buf []byte, size int, cam *camera.Camera, v *View)
}

// PhaseTimings breaks one WebGPU frame into pack/upload/shade/readback phases
// (milliseconds). Only the WebGPU backend implements PhaseTimingsProvider.
type PhaseTimings struct {
	Pack, Upload, GPU, Readback, Total float64
	Prims, Blockers, BVHNodes, Holes   int
}

// PhaseTimingsProvider is optionally implemented by the renderer so the HUD can
// show a live per-phase breakdown without importing the backend package.
type PhaseTimingsProvider interface {
	LastPhaseTimings() PhaseTimings
}

// GPUWorkload holds smoothed per-frame shader workload rates for the in-game
// HUD. These are sampled from GPU atomic counters (~once per second) and are a
// better signal than raw FPS on fast GPUs: they scale predictably to slower
// hardware and survive vsync caps.
//
// HUD maintenance: counters, cost weights (internal/webgpu/cost_model.go), and
// which fields we surface should be revisited whenever perf work shifts — e.g.
// panorama far-field, terrain mip pyramids, TLAS LOD, or CPU pack/upload if
// streaming lands. The line-1 rates and line-2 ~time mix are tuned for the
// current villa/lake bottleneck (terrain shadow march + BVH + mirrors/glass).
type GPUWorkload struct {
	Ready bool

	// Line 1 — raw workload rates (per pixel, smoothed).
	PathSegsPerPx       float64 // Ray-stack segments evaluated; >1 means bounces/reflections. Watch when toggling mirrors or glass.
	ShadowRaysPerPx     float64 // Shadow tests toward point lights (zero when shadows off). High ⇒ many lights × shaded surfaces.
	TerrainStepsPerTest float64 // Heightfield march steps per shadow ray or terrain hit. Dominant cost driver outdoors; spikes ⇒ terrain shadow march pain.
	MirrorBouncePerPx   float64 // Mirror/metal reflection rays spawned per pixel.
	GlassBouncePerPx    float64 // Glass reflection+refraction rays spawned per pixel (can fork the stack).

	// Line 2 — estimated GPU time mix (% of shader work), not screen coverage.
	// Derived from weighted counters (hits, terrain steps, shadow BVH tests, shading
	// split across surface types). Not measured GPU timestamps — treat as a
	// compass, not ground truth. Sky is omitted (procedural sky is negligible vs
	// terrain/geometry). Percentages sum to ~100 across the four buckets below.
	//
	// TimeTerrainPct — terrain heightfield marching (mostly shadow rays walking
	// the heightmap) plus terrain hits and their share of diffuse shading.
	// TimeInstPct — TLAS/BLAS traversal and shading for instanced geometry
	// (trees, etc.), plus a share of shadow-ray BVH cost attributed to instances.
	// TimeWaterPct — water-surface hits and shading (usually small despite large
	// screen area because the intersection is cheap).
	// TimePrimPct — static (non-instanced) primitive BVH hits and shading, plus
	// a share of shadow-ray BVH cost attributed to static blockers (villa mesh).
	TimeTerrainPct float64
	TimeInstPct    float64
	TimeWaterPct   float64
	TimePrimPct    float64

	// ShadowOccPct is the fraction of shadow rays that hit an occluder (BVH
	// blocker, plane, or terrain) before reaching the light — "shadow occlusion"
	// in the HUD. High ⇒ many rays exit early (good). Low ⇒ most rays march the
	// full distance (expensive, often open terrain or clear sightlines).
	ShadowOccPct float64

	// Intentionally not on the HUD today (add when they become hot paths):
	// DiffuseRefl bounces (semi-glossy walls), AO volume sampling cost, CPU
	// pack/upload/readback, instance/placement counts, glass vs mirror split
	// beyond bounce/px.
}

// GPUWorkloadProvider is optionally implemented by the WebGPU backend.
type GPUWorkloadProvider interface {
	LastGPUWorkload() GPUWorkload
}

// LiveWorkloadController enables periodic in-game shader counter sampling.
type LiveWorkloadController interface {
	SetLiveWorkload(on bool)
}

// FormatFrameBudget renders the primary timing signal: the true GPU compute
// cost per frame and the actual presented frame rate. With CPU/GPU pipelining
// the GPU no longer bounds the loop, so fps (real, vsync-limited) and the GPU
// ms (the GPU-bound ceiling ≈ 1000/gpuMS) are reported separately.
func FormatFrameBudget(gpuMS, fps float64) string {
	gpu := "gpu —"
	if gpuMS > 0 {
		gpu = fmt.Sprintf("gpu %.1f ms (%.0f fps cap)", gpuMS, 1000.0/gpuMS)
	}
	if fps > 0 {
		return fmt.Sprintf("%s · %.0f fps", gpu, fps)
	}
	return gpu
}

// FormatWorkloadHUD renders a compact two-line workload summary for the HUD.
func FormatWorkloadHUD(w GPUWorkload) (line1, line2 string) {
	line1 = fmt.Sprintf(
		"%.1f paths/px  %.1f shadows/px  %.0f terrain steps  mir %.2f  glass %.2f bounce/px",
		w.PathSegsPerPx, w.ShadowRaysPerPx, w.TerrainStepsPerTest,
		w.MirrorBouncePerPx, w.GlassBouncePerPx,
	)
	line2 = fmt.Sprintf(
		"~time terr %.0f%% inst %.0f%% water %.0f%% prim %.0f%%",
		w.TimeTerrainPct, w.TimeInstPct, w.TimeWaterPct, w.TimePrimPct,
	)
	if w.ShadowRaysPerPx > 0.05 {
		line2 += fmt.Sprintf("  sh occ %.0f%%", w.ShadowOccPct)
	}
	return line1, line2
}

// BackendNamer is optionally implemented by a renderer to report its backend
// name for the HUD (e.g. "webgpu").
type BackendNamer interface {
	BackendName() string
}
