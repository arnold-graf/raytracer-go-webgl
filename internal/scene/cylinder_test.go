package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestCylinderTaperedSideHit(t *testing.T) {
	c := Cylinder{CX: 0, CZ: 0, Radius: 1.0, RadiusTop: 0.5, YMin: 0, YMax: 4}
	// Ray grazing the mid-height surface (r=0.75 at y=2).
	r := vec.Ray{Origin: vec.V{X: 0.74, Y: 2, Z: -5}, Dir: vec.V{Z: 1}}
	tHit := c.Intersect(r)
	if tHit == Inf || tHit < 0 {
		t.Fatalf("expected side hit, got %v", tHit)
	}
	p := r.At(tHit)
	if math.Abs(p.Y-2) > 0.05 {
		t.Fatalf("hit y = %v, want ~2", p.Y)
	}
}

func TestCylinderUniformMatchesLegacy(t *testing.T) {
	c := Cylinder{CX: 1, CZ: 2, Radius: 0.4, YMin: 0, YMax: 3}
	r := vec.Ray{Origin: vec.V{X: 1, Y: 1.5, Z: -2}, Dir: vec.V{Z: 1}}
	t0 := c.Intersect(r)
	c.RadiusTop = c.Radius
	t1 := c.Intersect(r)
	if math.Abs(t0-t1) > 1e-9 {
		t.Fatalf("uniform taper changed hit: %v vs %v", t0, t1)
	}
}
