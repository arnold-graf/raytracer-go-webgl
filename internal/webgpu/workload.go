package webgpu

import (
	"raytracer/internal/render"
)

const workloadProfileInterval = 48 // sample counters ~once per second at 60 Hz

// SetLiveWorkload enables periodic shader-counter sampling for the in-game HUD.
// Sampling one frame every workloadProfileInterval adds modest GPU overhead.
func (r *Renderer) SetLiveWorkload(on bool) { r.liveWorkload = on }

// LastGPUWorkload implements render.GPUWorkloadProvider.
func (r *Renderer) LastGPUWorkload() render.GPUWorkload { return r.workload }

func (r *Renderer) maybeProfileWorkload(rp *renderParams) {
	if r.profiling {
		rp.profileEnabled = true
		return
	}
	if !r.liveWorkload {
		return
	}
	r.workloadFrame++
	if r.workloadFrame >= workloadProfileInterval {
		r.workloadFrame = 0
		rp.profileEnabled = true
	}
}

func (r *Renderer) absorbWorkloadSample(c GPUProfileCounters) {
	snap := workloadFromCounters(c)
	if !r.workload.Ready {
		r.workload = snap
		return
	}
	const alpha = 0.4
	blend := func(cur, next float64) float64 {
		return cur + alpha*(next-cur)
	}
	w := &r.workload
	w.PathSegsPerPx = blend(w.PathSegsPerPx, snap.PathSegsPerPx)
	w.ShadowRaysPerPx = blend(w.ShadowRaysPerPx, snap.ShadowRaysPerPx)
	w.TerrainStepsPerTest = blend(w.TerrainStepsPerTest, snap.TerrainStepsPerTest)
	w.MirrorBouncePerPx = blend(w.MirrorBouncePerPx, snap.MirrorBouncePerPx)
	w.GlassBouncePerPx = blend(w.GlassBouncePerPx, snap.GlassBouncePerPx)
	w.TimeTerrainPct = blend(w.TimeTerrainPct, snap.TimeTerrainPct)
	w.TimeInstPct = blend(w.TimeInstPct, snap.TimeInstPct)
	w.TimeWaterPct = blend(w.TimeWaterPct, snap.TimeWaterPct)
	w.TimePrimPct = blend(w.TimePrimPct, snap.TimePrimPct)
	w.ShadowOccPct = blend(w.ShadowOccPct, snap.ShadowOccPct)
	w.BVHStepsPerRay = blend(w.BVHStepsPerRay, snap.BVHStepsPerRay)
	w.PrimTestsPerRay = blend(w.PrimTestsPerRay, snap.PrimTestsPerRay)
}

func workloadFromCounters(c GPUProfileCounters) render.GPUWorkload {
	pix := float64(c.Pixels)
	if pix < 1 {
		pix = 1
	}
	terrainTests := float64(c.ShadowRays + c.HitTerrain)
	if terrainTests < 1 {
		terrainTests = 1
	}
	shadowOcc := 0.0
	if c.ShadowRays > 0 {
		shadowOcc = 100 * float64(c.ShadowBlock) / float64(c.ShadowRays)
	}
	rays := float64(maxU32(c.PathSegs+c.ShadowRays, 1))
	terrain, inst, water, prim := timeMixFromCounters(c)
	return render.GPUWorkload{
		Ready:               true,
		PathSegsPerPx:       float64(c.PathSegs) / pix,
		ShadowRaysPerPx:     float64(c.ShadowRays) / pix,
		TerrainStepsPerTest: float64(c.TerrainSteps) / terrainTests,
		MirrorBouncePerPx:   float64(c.MirrorBounces) / pix,
		GlassBouncePerPx:    float64(c.GlassBounces) / pix,
		TimeTerrainPct:      terrain,
		TimeInstPct:         inst,
		TimeWaterPct:        water,
		TimePrimPct:         prim,
		ShadowOccPct:        shadowOcc,
		BVHStepsPerRay:      float64(c.BVHSteps) / rays,
		PrimTestsPerRay:     float64(c.PrimTests) / rays,
	}
}
