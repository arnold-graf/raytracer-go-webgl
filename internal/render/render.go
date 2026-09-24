// Package render defines the backend contract the app draws through. The only
// implementation is the WebGPU renderer (internal/webgpu); this package holds
// the renderer interface plus the small, backend-agnostic types it exchanges
// with the app (the per-frame View, profiling timings, and the backend name).
package render

import (
	"fmt"
	"strings"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
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
	AOData probe.AOData
	AOok   bool
	// Bounds for the live GI probe grid: the built environment, excluding
	// the outsized primitives that dominate the scene's raw extent. Set from
	// probe.Probe.LitBounds. Zero-value GIBoundsOK falls back to the AO
	// volume's own box.
	GIMin, GIMax vec.V
	GIBoundsOK   bool
	AOVersion    uint64 // bumps when AOData is replaced (e.g. after an async bake)

	// ColorQuant selects the post-dither color depth: 0 = 8-bit dither, 1 = 15-bit (default),
	// 2 = crush (24 levels/ch), 4 = path-tracer grain instead of the ordered dither.
	// 3 is raw RGB, used by portal capture rather than offered in the key-5 cycle.
	ColorQuant uint32

	// AdaptiveAA enables neighbor-detected edge supersampling (two-pass).
	AdaptiveAA bool

	// ThinGlassGhost enables the second-pane reflection lobe on default thin
	// glass (double-pane look). It kept key 8 until the diffuse bounce took the
	// binding, and is now always on.
	ThinGlassGhost bool

	// BounceRays is how many cosine-weighted indirect rays each primary diffuse
	// hit casts. 0 is off, which is the default; key 8 toggles it. One extra
	// bounce only -- the second hit gathers direct light and stops. See
	// docs/diffuse-bounce.md.
	BounceRays int

	// BounceAmbient is the share of the scene's ambient constant kept while the
	// bounce is on. The two model the same thing, so 0 (the default) swaps one
	// for the other instead of adding them.
	BounceAmbient float64

	// BounceTemporal accumulates the indirect term across frames, reprojecting
	// the previous estimate through the camera's motion. This is what turns a
	// 2-ray estimate into an effective sample count in the dozens while the
	// camera is still.
	BounceTemporal bool

	// BounceAtrous is how many edge-avoiding wavelet passes filter what
	// temporal accumulation leaves, 0..4. It is what covers the frames right
	// after a disocclusion, when there is no history to reproject.
	BounceAtrous int

	// BounceShadowRR is the display-level contribution at which a light's
	// shadow ray is always traced from a bounce hit; below it the ray is
	// rouletted, unbiased, so the saving costs variance rather than accuracy.
	// 0 traces every one. See DefaultBounceShadowRR.
	BounceShadowRR float64

	// BounceBlueNoise swaps the white-noise direction sampler for a
	// low-discrepancy one (R2 over frames and samples, rotated by a
	// screen-space interleaved-gradient offset). Same ray count, less variance.
	BounceBlueNoise bool

	// BounceHalf gathers a hemisphere on half the pixels each frame, in a
	// checkerboard that inverts every frame; the rest keep their reprojected
	// history. Requires BounceTemporal, and is ignored without it.
	BounceHalf bool

	// BounceHalfTile makes that checkerboard one 8x8 workgroup per cell rather
	// than one pixel, so whole SIMD groups skip the gather instead of masking
	// half their lanes.
	BounceHalfTile bool

	// BounceHistNearest forces the history fetch back to a single nearest tap,
	// so the bilinear gather can be measured against what it replaced.
	BounceHistNearest bool

	// BounceMinify discounts a reprojected history sample by how badly the
	// fetch undersampled it. Targets backing away, where the previous frame was
	// magnified relative to now.
	BounceMinify bool

	// BounceVelHistory shortens the running mean in proportion to camera speed.
	BounceVelHistory bool

	// BounceReuse seeds a freshly disoccluded pixel from a converged neighbour
	// on the same surface, rather than leaving it on its own one-or-two-sample
	// estimate for the a-trous to smear.
	BounceReuse bool

	// BounceAdapt spends extra bounce rays on points that were outside the
	// previous frame's frustum and so have no history to average.
	BounceAdapt bool

	// BounceCoherent rotates the bounce sampler per tile rather than per pixel,
	// so neighbouring pixels trace near-parallel rays.
	BounceCoherent bool

	// BounceSpread is how much of the per-pixel rotation survives inside a
	// coherent tile, 0..1. It is what stops a tile being 16 copies of one
	// sample; see BOUNCE_SPREAD.
	BounceSpread float64

	// BounceTile is that tile's edge in pixels. 0 takes the shader default.
	// Larger is faster and shares more of a tile's noise, which reads as cloud
	// until the temporal filter has averaged the tile out.
	BounceTile float64

	// BounceClamp is how many standard deviations of the current neighbourhood
	// the reprojected history may sit outside before it is pulled back in.
	// 0 disables the clamp. See DefaultBounceClamp.
	BounceClamp float64

	// BounceFilter tunes the reconstruction. Any element left at zero takes the
	// corresponding DefaultBounceFilter value, so a caller can set one knob
	// without restating the rest.
	BounceFilter [4]float64

	// MaxBounceDepth caps mirror/glass/water recursion in the tracer (default 2 when 0).
	// Raised while the spyglass is up so rays can pass through its lenses and scene glass.
	MaxBounceDepth uint32
}

// DefaultBounceFilter is the reconstruction's tuning, indexed as the shader's
// params.bounce_filt: (temporal alpha floor, max history, phi_normal,
// phi_depth).
//
//	alpha 0.05   Floor on the newest frame's weight in the running mean. Below
//	             the 1/(len+1) true-average rate it does nothing; above it, it
//	             is what stops a *stale* history from outvoting a real lighting
//	             change forever. 0.05 is a 20-frame effective window.
//	history 32   Cap on the running mean's length. SVGF ships 32 for the same
//	             reason: an uncapped mean keeps converging, which sounds good
//	             until the light moves and the pixel takes a thousand frames to
//	             notice. It also bounds how wrong a bad reprojection can be.
//	normal 64    Exponent on dot(n, n_tap). Steep: two surfaces at 10 degrees
//	             already share almost no indirect light in a corner, which is
//	             exactly where over-blurring shows as a glow across the seam.
//	depth 0.05   Relative depth tolerance for a tap. Relative, not absolute,
//	             because these scenes span 200 world units and a fixed epsilon
//	             is either useless near the camera or blind far from it.
var DefaultBounceFilter = [4]float64{0.05, 32, 64, 0.05}

// DefaultBounceShadowRR is where a bounce hit's shadow rays stop being traced
// unconditionally, in display levels.
//
// It is a variance/time dial, not a quality one: the estimator is unbiased at
// any setting, so raising it does not darken or brighten anything, it only
// moves more of the indirect term's shadowing into noise for the a-trous pass
// to absorb.
//
// Swept on the villa hearth at 1024x640, interleaved, against tracing every
// ray. The cost column is RMS deviation from a 16-ray reference *with no
// temporal history*, which is the honest case -- a settled camera hides this
// entirely:
//
//	 0   233.0 ms          RMS 0.00894   trace every shadow ray
//	12   207.7 ms (-10.8%) RMS 0.00890
//	24   197.9 ms (-15.1%) RMS 0.00892   ← default
//	48   193.2 ms (-17.1%) RMS 0.00913   first measurable variance, little left to win
//
// 24 is where the curve flattens. Past it the saving is 2% and the noise starts
// to move. The same sweep on the office atrium is only -2.8%, because bounce
// hits there already cast 0.54 shadow rays each and there is nothing to
// roulette -- the win is specific to dim interiors with many small lights,
// which is exactly where the bounce was most expensive.
var DefaultBounceShadowRR = 24.0

// DefaultBounceClamp is the neighbourhood clamp's width, in standard deviations
// of the local spread. **Off by default, because it was measured not to work
// here**, and the reason is worth keeping.
//
// Clamping the reprojected history to the current neighbourhood is the standard
// anti-ghosting tool, and it assumes the neighbourhood is a more trustworthy
// picture of "what is here now" than the history is. At 2 samples per pixel it
// is not. The bias that actually shows up under motion is a broad, smooth ~0.4
// display levels across a large surface; the 3x3 neighbourhood it would be
// compared against has a standard deviation of several levels. The clamp cannot
// see the error, and all it does is throw away temporal convergence.
//
// Measured on the villa hearth, moving backwards at 0.4 units/frame. The middle
// column is the ghosting the clamp is supposed to remove:
//
//	gamma   still low-freq   moving low-freq
//	0             0.076            0.403
//	1.25          0.678            0.474
//	2             0.300            0.415
//	3             0.158            0.405
//
// The moving column barely moves at any width, while the still column degrades
// by up to 9x. Every apparent "improvement" in a ghosting-over-floor ratio was
// the floor rising, not the ghosting falling.
//
// Kept, because the argument changes at higher sample counts: the tool is sound
// and it is the neighbourhood's variance that is not. Raise it if the bounce
// ever runs at 8+ rays.
var DefaultBounceClamp = 0.0

// Renderer is the drawing backend the app depends on. Render fills buf
// (len = W*H*4, RGBA) by rendering v from cam. pixSize is a quality/speed knob
// (1 = full resolution) that backends may honor or ignore.
type Renderer interface {
	Render(buf []byte, cam *camera.Camera, v *View, pixSize int)
}

// DocumentTexturesInvalidator is optionally implemented by renderers that cache
// uploaded document text textures and need a nudge after scene reload.
type DocumentTexturesInvalidator interface {
	InvalidateDocumentTextures()
}

// DocumentTexturesSyncer is optionally implemented by renderers that can push
// document text textures to the GPU immediately (e.g. after scene reload).
type DocumentTexturesSyncer interface {
	DocumentTexturesInvalidator
	SyncDocumentTextures()
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

	// BVH quality (Representative-Ray-Set style), averaged over all rays cast
	// (primary + bounce + shadow). BVHStepsPerRay is node visits per ray and
	// PrimTestsPerRay is leaf primitive-intersection tests per ray. Lower is a
	// better tree; this is the direct signal to watch when evaluating any BVH
	// build change (bin count, spatial splits) — it moves independently of
	// shading cost, unlike raw fps.
	BVHStepsPerRay  float64
	PrimTestsPerRay float64

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
// cost per frame (gpuMS) with the GPU-bound ceiling in parentheses (max fps if
// the GPU were the only limiter), the measured trace rate (fps), and the active
// H-key FPS cap (capFPS, 0 = uncapped).
func FormatFrameBudget(gpuMS, fps float64, capFPS int) string {
	gpu := "gpu —"
	if gpuMS > 0 {
		gpu = fmt.Sprintf("gpu %.1f ms (max %.0f)", gpuMS, 1000.0/gpuMS)
	}
	out := gpu
	if fps > 0 {
		out = fmt.Sprintf("%s · %.0f fps", gpu, fps)
	}
	if capFPS > 0 {
		out += fmt.Sprintf(" · cap %d", capFPS)
	} else {
		out += " · uncapped"
	}
	return out
}

// FormatWorkloadHUD renders a compact two-line workload summary for the HUD.
// Both lines are adaptive: metrics that round to zero for the current scene
// (terrain/water/inst indoors, reflections when mirrors are off) are dropped so
// the overlay stays small and only shows what's actually costing time.
func FormatWorkloadHUD(w GPUWorkload) (line1, line2 string) {
	line1 = fmt.Sprintf(
		"%.1f paths/px  %.1f shadows/px  bvh %.1f steps %.1f tests/ray",
		w.PathSegsPerPx, w.ShadowRaysPerPx, w.BVHStepsPerRay, w.PrimTestsPerRay,
	)
	if w.MirrorBouncePerPx > 0.005 || w.GlassBouncePerPx > 0.005 {
		line1 += fmt.Sprintf("  mir %.2f glass %.2f/px", w.MirrorBouncePerPx, w.GlassBouncePerPx)
	}
	if w.TerrainStepsPerTest > 0.05 {
		line1 += fmt.Sprintf("  %.0f terrain steps", w.TerrainStepsPerTest)
	}

	var parts []string
	if w.TimeTerrainPct >= 0.5 {
		parts = append(parts, fmt.Sprintf("terr %.0f%%", w.TimeTerrainPct))
	}
	if w.TimeInstPct >= 0.5 {
		parts = append(parts, fmt.Sprintf("inst %.0f%%", w.TimeInstPct))
	}
	if w.TimeWaterPct >= 0.5 {
		parts = append(parts, fmt.Sprintf("water %.0f%%", w.TimeWaterPct))
	}
	if w.TimePrimPct >= 0.5 {
		parts = append(parts, fmt.Sprintf("prim %.0f%%", w.TimePrimPct))
	}
	line2 = "~time " + strings.Join(parts, " ")
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
