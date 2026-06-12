package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestTransformLocalRayRoundTrip(t *testing.T) {
	xf := NewTransform(0, 45, 0, vec.V{})
	origin := vec.New(3, 1, 2)
	dir := vec.New(0.2, -0.5, 0.7).Normalize()
	r := vec.Ray{Origin: origin, Dir: dir}

	lr := xf.LocalRay(r)
	tHit := 2.5
	wp := r.At(tHit)
	lp := lr.At(tHit)
	got := xf.ToWorld(lp)
	if math.Abs(got.X-wp.X) > 1e-9 || math.Abs(got.Y-wp.Y) > 1e-9 || math.Abs(got.Z-wp.Z) > 1e-9 {
		t.Fatalf("world point mismatch: got %v want %v", got, wp)
	}
}

func TestTransformPivotPreservesPoint(t *testing.T) {
	pivot := vec.New(2, 5, -1)
	xf := NewTransform(15, -20, 30, pivot)
	got := xf.ToWorld(pivot)
	if math.Abs(got.X-pivot.X) > 1e-9 || math.Abs(got.Y-pivot.Y) > 1e-9 || math.Abs(got.Z-pivot.Z) > 1e-9 {
		t.Fatalf("pivot moved: got %v want %v", got, pivot)
	}
}

func TestTransformCompose(t *testing.T) {
	inner := NewTransform(0, 90, 0, vec.V{})
	outer := NewInstanceTransform(0, 0, 0, vec.New(10, 0, 0))
	c := outer.Compose(inner)
	p := vec.New(1, 0, 0)
	want := vec.New(10, 0, -1) // 90° Y then translate +10 X
	got := c.ToWorld(p)
	if math.Abs(got.X-want.X) > 1e-9 || math.Abs(got.Y-want.Y) > 1e-9 || math.Abs(got.Z-want.Z) > 1e-9 {
		t.Fatalf("compose: got %v want %v", got, want)
	}
}
