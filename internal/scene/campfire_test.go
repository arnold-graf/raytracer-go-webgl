package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func testFire() *Campfire {
	return &Campfire{
		Center:  vec.New(0, 0.5, 3),
		Color:   vec.New(3.6, 1.7, 0.55),
		Range:   14,
		Jitter:  0.16,
		Flicker: 0.45,
		Speed:   1,
	}
}

func TestCampfireLightIsWarmAndDeterministic(t *testing.T) {
	f := testFire()
	for j := 0; j < CampfireLights; j++ {
		pos, col := f.LightAt(j, 2.5)

		// Warm: red dominates green dominates blue.
		if !(col.X > col.Y && col.Y > col.Z) {
			t.Errorf("sub-light %d not warm: %+v", j, col)
		}
		// Deterministic: same query, same result (all render workers must agree).
		pos2, col2 := f.LightAt(j, 2.5)
		if pos != pos2 || col != col2 {
			t.Errorf("sub-light %d not deterministic", j)
		}
		// Stays near the fire core.
		if d := pos.Sub(f.Center).Len(); d > 1.5 {
			t.Errorf("sub-light %d strayed %.2f from center", j, d)
		}
	}
}

func TestCampfireFlickersOverTime(t *testing.T) {
	f := testFire()

	var posMoved, colChanged bool
	pos0, col0 := f.LightAt(0, 0)
	for _, tm := range []float64{0.05, 0.1, 0.2, 0.37, 0.5} {
		pos, col := f.LightAt(0, tm)
		if pos.Sub(pos0).Len() > 1e-3 {
			posMoved = true
		}
		if math.Abs(col.X-col0.X) > 1e-3 {
			colChanged = true
		}
	}
	if !posMoved {
		t.Error("campfire light position did not move over time")
	}
	if !colChanged {
		t.Error("campfire light intensity did not flicker over time")
	}
}

func TestCampfireBrightnessScalesIntensity(t *testing.T) {
	base := testFire()
	bright := testFire()
	bright.Brightness = 2

	for j := 0; j < CampfireLights; j++ {
		_, c1 := base.LightAt(j, 1.3)
		_, c2 := bright.LightAt(j, 1.3)
		// Brightness multiplies the color linearly, leaving hue and position
		// untouched.
		if math.Abs(c2.X-2*c1.X) > 1e-9 || math.Abs(c2.Y-2*c1.Y) > 1e-9 || math.Abs(c2.Z-2*c1.Z) > 1e-9 {
			t.Errorf("sub-light %d: brightness=2 gave %+v, want 2x %+v", j, c2, c1)
		}
	}
	if bright.PeakChannel() <= base.PeakChannel() {
		t.Error("brightness did not raise the cull bound")
	}
}

func TestCampfirePeakChannelBoundsColor(t *testing.T) {
	f := testFire()
	peak := f.PeakChannel()
	// Sample many times; no per-channel intensity should exceed the bound.
	for i := 0; i < 200; i++ {
		tm := float64(i) * 0.013
		for j := 0; j < CampfireLights; j++ {
			_, col := f.LightAt(j, tm)
			m := math.Max(col.X, math.Max(col.Y, col.Z))
			if m > peak+1e-9 {
				t.Fatalf("channel %.3f exceeded PeakChannel %.3f", m, peak)
			}
		}
	}
}
