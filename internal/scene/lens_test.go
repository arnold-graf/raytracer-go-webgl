package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestLensIntersect(t *testing.T) {
	l := Lens{
		CX: 0, CY: 0, CZ: 0,
		Aperture: 0.02, RFront: 0.04, RBack: 0.04, Thickness: 0.004,
		Surface: Surface{Mat: MatGlass, IOR: 1.5},
	}
	// Ray along optical axis from −Y should hit the front cap.
	r := vec.Ray{Origin: vec.New(0, -0.1, 0), Dir: vec.New(0, 1, 0)}
	t0 := l.Intersect(r)
	if math.IsInf(t0, 1) || t0 <= 0 {
		t.Fatalf("front hit t=%v, want finite > 0", t0)
	}
	hp := r.Origin.Add(r.Dir.Scale(t0))
	n := l.Normal(hp)
	if n.Y > -0.5 {
		t.Fatalf("front normal %v should point mostly −Y", n)
	}
	// Grazing miss outside aperture.
	r2 := vec.Ray{Origin: vec.New(0.05, -0.1, 0), Dir: vec.New(0, 1, 0)}
	if !math.IsInf(l.Intersect(r2), 1) {
		t.Fatal("expected miss outside aperture")
	}
}
