package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

// A ray aimed straight through a hole should pass through the box; a ray aimed
// at solid wall should hit the near face.
func TestBoxHolePassThrough(t *testing.T) {
	b := Box{
		Min:   vec.New(-1, 0, -1),
		Max:   vec.New(-0.6, 4, 1),
		Holes: []AABB{{Min: vec.New(-1.1, 1, -0.5), Max: vec.New(-0.5, 3, 0.5)}},
	}

	// Through the hole (y=2, z=0): should miss the box entirely.
	through := vec.Ray{Origin: vec.New(-5, 2, 0), Dir: vec.New(1, 0, 0)}
	if got := b.Intersect(through); got != Inf {
		t.Fatalf("ray through hole hit at t=%v, want miss", got)
	}

	// Below the hole (y=0.5): should hit the near face at x=-1, t=4.
	solid := vec.Ray{Origin: vec.New(-5, 0.5, 0), Dir: vec.New(1, 0, 0)}
	got := b.Intersect(solid)
	if math.Abs(got-4) > 1e-6 {
		t.Fatalf("ray at solid wall t=%v, want 4", got)
	}
}

// A ray skimming through the opening should hit the inner sill of the hole on
// the far side when the opening only partially clears the line of sight.
func TestBoxHoleInnerFaceNormal(t *testing.T) {
	b := Box{
		Min:   vec.New(0, 0, 0),
		Max:   vec.New(2, 4, 2),
		Holes: []AABB{{Min: vec.New(-0.1, 1, 0.5), Max: vec.New(2.1, 3, 1.5)}},
	}
	// Enter at x=0 below the hole, the near face is still solid at y=0.5.
	r := vec.Ray{Origin: vec.New(-3, 0.5, 1), Dir: vec.New(1, 0, 0)}
	tHit := b.Intersect(r)
	if tHit == Inf {
		t.Fatal("expected hit on solid lower wall")
	}
	n := b.Normal(r.At(tHit))
	if math.Abs(n.X) < 0.9 {
		t.Fatalf("expected X-facing normal, got %v", n)
	}
}
