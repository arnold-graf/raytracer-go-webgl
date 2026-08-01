package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestCappedConeHitsBaseDisk(t *testing.T) {
	c := Cone{CX: 0, CZ: 0, YBase: 0, YTip: 2, RBase: 1, Capped: true}
	r := vec.Ray{Origin: vec.V{X: 0, Y: -1, Z: 0}, Dir: vec.V{Y: 1}}
	tHit := c.Intersect(r)
	if math.IsInf(tHit, 1) {
		t.Fatal("expected base cap hit")
	}
	hy := r.Origin.Y + r.Dir.Y*tHit
	if math.Abs(hy-c.YBase) > 0.01 {
		t.Fatalf("hit y=%.3f, want base at %.3f", hy, c.YBase)
	}
}

func TestUncappedConeSkipsBaseDisk(t *testing.T) {
	c := Cone{CX: 0, CZ: 0, YBase: 0, YTip: 2, RBase: 1, Capped: false}
	r := vec.Ray{Origin: vec.V{X: 0, Y: -1, Z: 0}, Dir: vec.V{Y: 1}}
	tHit := c.Intersect(r)
	if math.IsInf(tHit, 1) {
		t.Fatal("expected side hit")
	}
	hy := r.Origin.Y + r.Dir.Y*tHit
	if math.Abs(hy-c.YBase) < 0.01 {
		t.Fatal("uncapped cone should not hit the base disk")
	}
}
