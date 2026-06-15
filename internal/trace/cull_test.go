package trace

import (
	"math"
	"testing"

	"raytracer/internal/gpuscene"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestLightCulling(t *testing.T) {
	s := &scene.Scene{
		Lights: []scene.Light{
			{Pos: vec.New(0, 0, 0), Color: vec.New(8, 6, 4)},            // auto reach
			{Pos: vec.New(0, 0, 0), Color: vec.New(8, 6, 4), Range: 12}, // explicit range
			{Pos: vec.New(0, 0, 0), Color: vec.New(0, 0, 0)},            // off → always culled
		},
	}
	tr := New(s)

	// Auto light: no window, cull distance from the contribution threshold.
	if tr.lightInvR2[0] != 0 {
		t.Errorf("auto light invR2 = %v, want 0 (no window)", tr.lightInvR2[0])
	}
	wantAuto := (8/lightCullEps - gpuscene.LightAttenBase) / gpuscene.LightAttenQuadratic
	if math.Abs(tr.lightCullR2[0]-wantAuto) > 1e-6 {
		t.Errorf("auto light cullR2 = %v, want %v", tr.lightCullR2[0], wantAuto)
	}

	// Explicit range: cull at Range^2 with a matching window factor.
	if got, want := tr.lightCullR2[1], 12.0*12.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("ranged light cullR2 = %v, want %v", got, want)
	}
	if got, want := tr.lightInvR2[1], 1.0/144.0; math.Abs(got-want) > 1e-12 {
		t.Errorf("ranged light invR2 = %v, want %v", got, want)
	}

	// Dark light contributes nothing and is always culled.
	if tr.lightCullR2[2] != 0 {
		t.Errorf("dark light cullR2 = %v, want 0", tr.lightCullR2[2])
	}
}
