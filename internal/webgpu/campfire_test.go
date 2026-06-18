package webgpu

import (
	"math"
	"testing"

	"raytracer/internal/gpuscene"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestCampfireSublightMatchesLightAt(t *testing.T) {
	fires := []scene.Campfire{
		{
			Center: vec.New(0, 0.42, 3), Color: vec.New(3.6, 1.7, 0.55),
			Brightness: 1, Range: 14, Jitter: 0.16, Flicker: 0.45, Speed: 1, Seed: 0,
		},
		{
			Center: vec.New(-6.35, 1.55, 2.35), Color: vec.New(5.0, 2.2, 0.8),
			Brightness: 0.35, Range: 14, Jitter: 0.16, Flicker: 0.6, Speed: 1.2, Seed: 2.5,
		},
		{
			Center: vec.New(1, 0.1, -2), Color: vec.New(1, 0.6, 0.3),
			Brightness: 5, Range: 0, Jitter: 0.02, Flicker: 0.4, Speed: 0, Seed: 1,
		},
	}
	times := []float64{0, 0.37, 1.7, 9.13, 42.0}
	const eps = 1e-4

	for fi, fire := range fires {
		sc := &scene.Scene{Campfires: []scene.Campfire{fire}}
		cf := PackCampfireParams(sc)[0]
		for j := range 3 {
			for _, tm := range times {
				wantPos, wantCol := fire.LightAt(j, tm)
				gotPos, gotCol := resolveCampfireSublight(cf, j, tm)
				if d := vecDist(wantPos, gotPos); d > eps {
					t.Fatalf("fire[%d] j=%d t=%v pos delta=%v\n  want %v\n  got  %v",
						fi, j, tm, d, wantPos, gotPos)
				}
				if d := vecDist(wantCol, gotCol); d > eps {
					t.Fatalf("fire[%d] j=%d t=%v color delta=%v\n  want %v\n  got  %v",
						fi, j, tm, d, wantCol, gotCol)
				}
			}
		}
	}
}

func TestCampfireCullExplicitRange(t *testing.T) {
	cf := CampfireParams{Core: [4]float32{0, 0, 0, 14}}
	cullR2, invR2 := campfireCull(cf)
	if math.Abs(cullR2-196) > 1e-9 || math.Abs(invR2-1.0/196) > 1e-9 {
		t.Fatalf("explicit range cull = (%v, %v), want (196, %v)", cullR2, invR2, 1.0/196)
	}
}

func TestCampfireCullAutoRange(t *testing.T) {
	fire := scene.Campfire{
		Center: vec.V{}, Color: vec.New(3.6, 1.7, 0.55),
		Brightness: 1, Flicker: 0.45, Range: 0,
	}
	sc := &scene.Scene{Campfires: []scene.Campfire{fire}}
	cf := PackCampfireParams(sc)[0]

	gotR2, gotInv := campfireCull(cf)
	wantR2, wantInv := fireCullLegacy(fire.PeakChannel(), 0)
	if math.Abs(gotR2-wantR2) > 1e-3 || math.Abs(gotInv-wantInv) > 1e-9 {
		t.Fatalf("auto cull = (%v, %v), want (%v, %v)", gotR2, gotInv, wantR2, wantInv)
	}
	if gotR2 <= 0 {
		t.Fatal("auto cull radius should be positive when range=0")
	}
}

// fireCullLegacy mirrors the removed PackCampfires cull helper.
func fireCullLegacy(peak, rng float64) (cullR2, invR2 float64) {
	if rng > 0 {
		r2 := rng * rng
		return r2, 1 / r2
	}
	if peak > gpuscene.LightCullEps*gpuscene.LightAttenBase {
		autoR2 := (peak/gpuscene.LightCullEps - gpuscene.LightAttenBase) / gpuscene.LightAttenQuadratic
		if autoR2 < 0 {
			autoR2 = 0
		}
		return autoR2, 0
	}
	return 0, 0
}

func vecDist(a, b vec.V) float64 {
	return math.Hypot(a.X-b.X, math.Hypot(a.Y-b.Y, a.Z-b.Z))
}
