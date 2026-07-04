package util

import (
	"testing"

	"raytracer/internal/vec"
)

func TestOverlap(t *testing.T) {
	a0, a1 := vec.V{}, vec.V{X: 1, Y: 1, Z: 1}
	b0, b1 := vec.V{X: 0.5, Y: 0.5, Z: 0.5}, vec.V{X: 1.5, Y: 1.5, Z: 1.5}
	if !Overlap(a0, a1, b0, b1, DefaultPenetration) {
		t.Fatal("expected overlap")
	}
	c0, c1 := vec.V{X: 2, Y: 0, Z: 0}, vec.V{X: 3, Y: 1, Z: 1}
	if Overlap(a0, a1, c0, c1, DefaultPenetration) {
		t.Fatal("expected separation")
	}
	// face-touch within penetration slack
	touch0, touch1 := vec.V{X: 1, Y: 0, Z: 0}, vec.V{X: 2, Y: 1, Z: 1}
	if Overlap(a0, a1, touch0, touch1, DefaultPenetration) {
		t.Fatal("face touch should not count as overlap")
	}
}
