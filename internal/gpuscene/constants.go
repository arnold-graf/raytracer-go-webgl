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

	// GammaLUTSize is the CPU gamma lookup resolution. The GPU path can either
	// mirror this exactly or use it in parity tests to confirm its approximation.
	GammaLUTSize = 4096

	// Wallpaper tile sizes are part of texture fidelity and must match WGSL.
	WallpaperTileW = 0.55
	WallpaperTileH = 0.775

	// BVH tuning constants. The CPU builds the tree, but WGSL traversal and
	// packing tests need these to stay named and synchronized.
	BVHLeafSize        = 2
	BVHSAHBins         = 32
	BVHSAHTraverseCost = 1.0 // relative traversal vs intersection cost in SAH splits

	// BVHStackSize is the depth of each WGSL depth-first traversal stack. These
	// stacks are per-thread scratch arrays and several are live at once along
	// the trace call chain, so their size directly costs GPU occupancy. A DFS
	// that pushes both children and pops one holds at most depth+1 entries;
	// TestBVHTraversalStackDepth measures every packed tree in scenes/ against
	// this bound.
	BVHStackSize = 32
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
