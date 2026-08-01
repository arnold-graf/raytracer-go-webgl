package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestFlameSystemSpawnsAndAdvances(t *testing.T) {
	fires := []Campfire{{
		Center: vec.V{Y: 0.5},
		Color:  vec.V{X: 3.6, Y: 1.7, Z: 0.55},
	}}
	var fs FlameSystem
	fs.SimulateTo(fires, 2.0)
	parts := fs.ActiveParticles()
	if len(parts) < 8 {
		t.Fatalf("got %d active particles after 2s, want a lively fire", len(parts))
	}
	for i, p := range parts {
		if p.Radius <= 0 {
			t.Fatalf("particle %d has non-positive radius", i)
		}
		if p.Color.X <= 0 && p.Color.Y <= 0 {
			t.Fatalf("particle %d has zero color", i)
		}
		if p.Color.X < p.Color.Y*0.9 {
			t.Fatalf("particle %d too yellow/white, want red-dominant fire: %v", i, p.Color)
		}
		if p.Color.Z > p.Color.X*0.25 {
			t.Fatalf("particle %d too blue/white for fire: %v", i, p.Color)
		}
	}
}

func TestFlameCustomColors(t *testing.T) {
	fires := []Campfire{{
		Flame:      true,
		FlameEmber: vec.V{X: 4, Y: 0.1, Z: 0},
		FlameMid:   vec.V{X: 3, Y: 0.5, Z: 0},
		FlameTip:   vec.V{X: 2, Y: 1.5, Z: 0.1},
		FlameAsh:   vec.V{X: 0.5, Y: 0.1, Z: 0},
	}}
	var fs FlameSystem
	fs.SimulateTo(fires, 0.5)
	parts := fs.ActiveParticles()
	if len(parts) == 0 {
		t.Fatal("expected particles")
	}
	// Young particles should stay close to the authored ember red.
	if parts[0].Color.X < 0.5 {
		t.Fatalf("ember color too dim: %v", parts[0].Color)
	}
	if parts[0].Color.Y > parts[0].Color.X {
		t.Fatalf("custom ember should be red-dominant: %v", parts[0].Color)
	}
}

func TestCampfireFlamePaletteDefaults(t *testing.T) {
	e, m, ti, a := (Campfire{}).flamePalette()
	if e != DefaultFlameEmber || m != DefaultFlameMid || ti != DefaultFlameTip || a != DefaultFlameAsh {
		t.Fatalf("defaults mismatch: %v %v %v %v", e, m, ti, a)
	}
	custom := Campfire{FlameMid: vec.V{1, 2, 3}}
	_, m, _, _ = custom.flamePalette()
	if m != (vec.V{X: 1, Y: 2, Z: 3}) {
		t.Fatalf("override mid = %v", m)
	}
}

func TestFlameSystemDeterministic(t *testing.T) {
	fires := []Campfire{{
		Center: vec.V{X: 1, Y: 0.4, Z: -2},
		Color:  vec.V{X: 4, Y: 2, Z: 0.6},
		Seed:   3.5,
	}}
	var a, b FlameSystem
	a.SimulateTo(fires, 1.5)
	b.SimulateTo(fires, 1.5)
	pa, pb := a.ActiveParticles(), b.ActiveParticles()
	if len(pa) != len(pb) {
		t.Fatalf("particle count mismatch: %d vs %d", len(pa), len(pb))
	}
	for i := range pa {
		if pa[i].Pos != pb[i].Pos || pa[i].Radius != pb[i].Radius || pa[i].Color != pb[i].Color {
			t.Fatalf("particle %d diverged", i)
		}
	}
}
