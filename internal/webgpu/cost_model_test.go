package webgpu

import (
	"math"
	"strings"
	"testing"

	"raytracer/internal/render"
)

func TestTimeMixFromCountersVillaLike(t *testing.T) {
	c := GPUProfileCounters{
		TerrainSteps: 11_477_265,
		HitTerrain:   56_717,
		HitPrim:      65_769,
		HitInst:      65_769,
		HitWater:     36_217,
		Sky:          4_250,
		ShadowRays:   539_851,
		PathSegs:     162_953,
	}
	terr, inst, water, prim := timeMixFromCounters(c)
	sum := terr + inst + water + prim
	if math.Abs(sum-100) > 0.5 {
		t.Fatalf("mix sum = %v, want 100", sum)
	}
	if terr < 55 {
		t.Fatalf("terrain time should dominate villa view, got terr=%.0f%% inst=%.0f%% water=%.0f%%",
			terr, inst, water)
	}
	if prim > 1 {
		t.Fatalf("static prim share should be ~0 in instanced villa, got %.0f%%", prim)
	}
}

func TestWorkloadFromCountersUsesTimeMix(t *testing.T) {
	c := GPUProfileCounters{
		Pixels: 100_000, PathSegs: 160_000,
		HitTerrain: 50_000, HitInst: 40_000, HitWater: 30_000, Sky: 1_000,
		ShadowRays: 500_000, TerrainSteps: 10_000_000,
	}
	w := workloadFromCounters(c)
	if w.TimeTerrainPct < 50 {
		t.Fatalf("TimeTerrainPct = %v, want dominant", w.TimeTerrainPct)
	}
	if w.TimeWaterPct > w.TimeTerrainPct {
		t.Fatalf("water screen share was high but time share should be lower: water=%.0f terrain=%.0f",
			w.TimeWaterPct, w.TimeTerrainPct)
	}
}

func TestFormatWorkloadHUD(t *testing.T) {
	line1, line2 := render.FormatWorkloadHUD(render.GPUWorkload{
		PathSegsPerPx: 1.6, ShadowRaysPerPx: 5.4, TerrainStepsPerTest: 19,
		MirrorBouncePerPx: 0.36, GlassBouncePerPx: 0.14, ShadowOccPct: 8,
		TimeTerrainPct: 62, TimeInstPct: 28, TimeWaterPct: 8, TimePrimPct: 2,
	})
	if !strings.Contains(line1, "1.6 paths/px") {
		t.Fatalf("line1 = %q", line1)
	}
	if !strings.Contains(line2, "~time") || !strings.Contains(line2, "terr 62%") || strings.Contains(line2, "sky") {
		t.Fatalf("line2 = %q", line2)
	}
	if !strings.Contains(line2, "sh occ 8%") {
		t.Fatalf("line2 = %q", line2)
	}
}
