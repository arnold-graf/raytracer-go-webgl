// Package gpuscene contains data-layout and constant definitions shared by the
// CPU renderer and the planned WebGPU packing/shader path. Keeping these values
// in one place reduces the chance of Go/WGSL drift during the port.
package gpuscene

import "fmt"

const (
	// RayEpsilon is the shared self-intersection guard for analytic primitives.
	RayEpsilon = 1e-4

	// LightCullEps is the per-channel radiance below which a point light's
	// (unshadowed, best-case) contribution is treated as invisible and culled.
	LightCullEps = 0.0025

	// Point-light attenuation is 1/(LightAttenBase + LightAttenQuadratic*d^2).
	LightAttenBase      = 0.5
	LightAttenQuadratic = 0.08

	// AOMaxDist is the baked ambient-occlusion probe radius.
	AOMaxDist = 0.9

	// Live GI grid. The megakernel already declares 30 storage/uniform
	// buffers and naga spends one more on the runtime-array sizes table,
	// which is exactly Metal's limit of 31 per stage — so the grid has no
	// binding of its own and lives in the tail of ao_volume instead.
	//
	// AOVolumeFloats is where the AO data ends and the probe field begins.
	AOVolumeFloats = 6_000_000

	// GIProbeFloats mirrors GI_PROBE_FLOATS in types.wesl: an 8x8 octahedral
	// irradiance map plus a 16x16 octahedral depth map.
	GIProbeFloats = 8*8*3 + 16*16*2
	// GIProbeMaxCells is what the two live cascades share: the fine one's
	// GI_C0_MAX reservation and the coarse one's cap.
	GIProbeMaxCells = 16384 + 2048
	// GIVolumeTotalFloats sizes ao_volume when the live cascades are on. With
	// GI off the buffer is cut to AOVolumeFloats alone, and with a bake it is
	// sized from that file's header — it is the scene that decides how many
	// probes it needs.
	GIVolumeTotalFloats = AOVolumeFloats + GIProbeMaxCells*GIProbeFloats

	// GammaLUTSize is the CPU gamma lookup resolution. The GPU path can either
	// mirror this exactly or use it in parity tests to confirm its approximation.
	GammaLUTSize = 4096

	// Wallpaper tile sizes are part of texture fidelity and must match WGSL.
	WallpaperTileW = 0.55
	WallpaperTileH = 0.775

	// BVH tuning constants. The CPU builds the tree, but WGSL traversal and
	// packing tests need these to stay named and synchronized.
	// BVHLeafSize is 1 rather than the conventional 2-4 because this tracer's
	// primitives are expensive to intersect (holed boxes, tori) while a node
	// visit is just a slab test, so it pays to split all the way down. Measured
	// on office-sunset: widening leaves to 4 cut node visits by only 8% but
	// nearly doubled primitive tests and cost 0.8%, while narrowing to 1 cut
	// primitive tests from 2.8 to 1.0 per ray for 8% more visits and gained
	// 2-5%. See docs/megakernel-optimization.md.
	BVHLeafSize = 1
	BVHSAHBins  = 32
	// BVHSAHTraverseCost only shifts every split candidate by the same amount,
	// so it cannot change which split wins; the builders also always split down
	// to BVHLeafSize instead of comparing split cost against leaf cost. It is
	// kept because both SAH implementations reference it, but tuning it is a
	// no-op until they compare against a leaf alternative.
	BVHSAHTraverseCost = 1.0

	// BVHStackSize is the depth of each WGSL depth-first traversal stack. These
	// stacks are per-thread scratch arrays and several are live at once along
	// the trace call chain, so their size directly costs GPU occupancy. A DFS
	// that pushes both children and pops one holds at most depth+1 entries;
	// TestBVHTraversalStackDepth measures every packed tree in scenes/ against
	// this bound.
	//
	// 24 because the deepest tree in scenes/ is 18 levels, so 19 is the
	// requirement. It was 32, which cost 1.4 ms a frame on the office-sunset
	// atrium view for headroom nothing uses; see the longer note on
	// BVH_STACK_SIZE in shaders/modules/bvh.wesl.
	BVHStackSize = 24
)

// WGSLConstants emits the shader-side constants that must stay synchronized with
// the CPU. It is intentionally boring string formatting so tests can diff it
// before real WGSL files exist.
func WGSLConstants() string {
	return fmt.Sprintf(`const RAY_EPSILON: f32 = %.9g;
const LIGHT_CULL_EPS: f32 = %.9g;
const LIGHT_ATTEN_BASE: f32 = %.9g;
const LIGHT_ATTEN_QUADRATIC: f32 = %.9g;
const AO_MAX_DIST: f32 = %.9g;
const GAMMA_LUT_SIZE: u32 = %d;
const WALLPAPER_TILE_W: f32 = %.9g;
const WALLPAPER_TILE_H: f32 = %.9g;
const BVH_LEAF_SIZE: u32 = %d;
const BVH_SAH_BINS: u32 = %d;
`,
		RayEpsilon,
		LightCullEps,
		LightAttenBase,
		LightAttenQuadratic,
		AOMaxDist,
		GammaLUTSize,
		WallpaperTileW,
		WallpaperTileH,
		BVHLeafSize,
		BVHSAHBins,
	)
}
