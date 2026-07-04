package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestRingIntersect(t *testing.T) {
	rg := Ring{CX: 0, CY: -2, CZ: 0, Radius: 0.5, Height: 0.03}
	ray := vec.Ray{Origin: vec.New(0.55, -2, 0), Dir: vec.New(-1, 0, 0)}
	tHit := rg.Intersect(ray)
	if math.IsInf(tHit, 0) || tHit < 0.04 || tHit > 0.06 {
		t.Fatalf("side hit t=%v, want ~0.05", tHit)
	}
	hp := ray.Origin.Add(ray.Dir.Scale(tHit))
	n := rg.Normal(hp)
	if math.Abs(n.Y) > 1e-3 || n.Len() < 0.99 {
		t.Fatalf("normal = %+v, want horizontal unit", n)
	}
	miss := vec.Ray{Origin: vec.New(0, 0, 0), Dir: vec.New(1, -1, 0).Normalize()}
	if !math.IsInf(rg.Intersect(miss), 1) {
		t.Fatal("off-ring ray should miss")
	}
}
