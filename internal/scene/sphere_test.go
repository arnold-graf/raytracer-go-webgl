package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestSphereCutOffOpenBottom(t *testing.T) {
	s := Sphere{Center: vec.V{}, Radius: 1, CutOff: 0.5}

	// Straight up through the opening should not hit a cap at the cut plane.
	r := vec.Ray{Origin: vec.V{X: 0, Y: -2, Z: 0}, Dir: vec.V{Y: 1}}
	tHit := s.Intersect(r)
	if math.IsInf(tHit, 1) {
		t.Fatal("expected hit on dome interior from below")
	}
	hp := r.At(tHit)
	if hp.Y <= 0 {
		t.Fatalf("hit y = %v, want above cut plane", hp.Y)
	}

	// Angled ray from below should hit the curved dome wall.
	r2 := vec.Ray{Origin: vec.V{X: 0.2, Y: -0.5, Z: 0}, Dir: vec.V{Y: 1}}
	if math.IsInf(s.Intersect(r2), 1) {
		t.Fatal("expected hit on dome interior from below")
	}
}

func TestSphereCutOffMissesRemovedBottom(t *testing.T) {
	s := Sphere{Center: vec.V{}, Radius: 1, CutOff: 0.5}
	r := vec.Ray{Origin: vec.V{X: 0, Y: -2, Z: 0}, Dir: vec.V{X: 0.3, Y: 1, Z: 0}.Normalize()}
	tHit := s.Intersect(r)
	if !math.IsInf(tHit, 1) {
		hp := r.At(tHit)
		if hp.Y < -1e-6 {
			t.Fatalf("hit below cut plane at y=%v", hp.Y)
		}
	}
}

func TestSphereCutOffZeroIsFullSphere(t *testing.T) {
	s := Sphere{Center: vec.V{}, Radius: 1}
	r := vec.Ray{Origin: vec.V{X: 0, Y: 0, Z: -2}, Dir: vec.V{Z: 1}}
	if got := s.Intersect(r); math.Abs(got-1) > 1e-9 {
		t.Fatalf("full sphere t = %v, want 1", got)
	}
}

func TestSphereCutPlaneAtPercent(t *testing.T) {
	s := Sphere{Center: vec.V{Y: 1}, Radius: 2, CutOff: 0.5}
	if y := s.cutPlaneY(); math.Abs(y-1) > 1e-9 {
		t.Fatalf("cut plane y = %v, want 1", y)
	}
}
