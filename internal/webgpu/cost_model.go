package webgpu

// Relative GPU cost weights for HUD time-mix estimates. Originally calibrated
// against gpuprof ablations on outdoors-night-villa: terrain march steps
// dominate shadow rays; instanced BVH is next; sky/water are cheap per event.
//
// KNOWN WRONG for terrain, by roughly an order of magnitude. Direct measurement
// on outdoors-night-villa's default camera (2026-09) contradicts the weight
// below: cutting terrain march steps by 33% (5.7M to 3.8M, via the scene's
// `step` knob) changed frame time by 2.4%, and coarsening the baked height grid
// 16x (0.25 m to 1.0 m cells) changed it by 0.0%. Terrain marching is a few
// percent of that frame. The mix line reports 50%, because costTerrainStep is
// multiplied by a step count in the millions while every other term is
// multiplied by a hit count in the hundreds of thousands.
//
// The practical consequence is that this mix will point at terrain in any scene
// with a large heightfield, whether or not terrain is the problem. Treat it as
// a coverage hint, not a profile; use `-ablate` and targeted scene probes for
// anything load-bearing. Recalibrating properly needs per-event measurements for
// the other classes too, which nobody has taken yet — see
// docs/terrain-cost.md.
const (
	costTerrainStep  = 1.0
	costTerrainHit   = 4.0
	costInstHit      = 12.0
	costPrimHit      = 10.0
	costWaterHit     = 3.0
	costShadowRayBVH = 6.0
	costPathShade    = 2.0 // diffuse lights, AO, setup per path segment
)

// timeMixFromCounters estimates what fraction of GPU shader time each surface
// class accounts for. Sky is excluded from the mix (negligible cost). This is
// not screen coverage — terrain can be a sliver of pixels but most of the frame
// time when shadow rays march the heightfield.
func timeMixFromCounters(c GPUProfileCounters) (terrain, inst, water, prim float64) {
	primHits := int64(c.HitPrim) - int64(c.HitInst)
	if primHits < 0 {
		primHits = 0
	}
	instHits := int64(c.HitInst)
	primTotal := instHits + primHits
	if primTotal < 1 {
		primTotal = 1
	}

	shadowInst := float64(c.ShadowRays) * costShadowRayBVH * float64(instHits) / float64(primTotal)
	shadowPrim := float64(c.ShadowRays) * costShadowRayBVH * float64(primHits) / float64(primTotal)

	terrainCost := float64(c.TerrainSteps)*costTerrainStep + float64(c.HitTerrain)*costTerrainHit
	instCost := float64(instHits)*costInstHit + shadowInst
	primCost := float64(primHits)*costPrimHit + shadowPrim
	waterCost := float64(c.HitWater) * costWaterHit

	hitTotal := float64(c.HitTerrain + c.HitPrim + c.HitWater + c.Sky)
	if hitTotal < 1 {
		hitTotal = 1
	}
	shade := float64(c.PathSegs) * costPathShade
	terrainCost += shade * float64(c.HitTerrain) / hitTotal
	instCost += shade * float64(instHits) / hitTotal
	primCost += shade * float64(primHits) / hitTotal
	waterCost += shade * float64(c.HitWater) / hitTotal
	// Sky shading is folded into hitTotal for splitting but not shown in the mix.

	total := terrainCost + instCost + primCost + waterCost
	if total < 1e-9 {
		return 0, 0, 0, 0
	}
	scale := 100 / total
	return terrainCost * scale, instCost * scale, waterCost * scale, primCost * scale
}
