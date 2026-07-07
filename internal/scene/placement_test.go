package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestPlacementTransformAnchorAt(t *testing.T) {
	origin := vec.New(1, 2, 3)
	at := vec.New(4, 5, 6)
	xf := PlacementTransform(15, 30, 45, at, origin)
	got := xf.ToWorld(origin)
	if got.Sub(at).Len() > 1e-9 {
		t.Fatalf("world(origin) = %v, want %v", got, at)
	}
}

func TestPlacementTransformParityMigration(t *testing.T) {
	center := vec.New(0.4, 1.2, -0.3)
	oldAt := vec.New(8, 0.2, 9)
	rx, ry, rz := 0.0, 20.0, 0.0

	old := NewInstanceTransform(rx, ry, rz, oldAt)
	newAt := MigratedIncludeAt(oldAt, rx, ry, rz, center)
	newXf := PlacementTransform(rx, ry, rz, newAt, center)

	tests := []vec.V{
		vec.V{},
		center,
		vec.New(1, 0, 0),
		vec.New(-0.2, 2.5, 0.7),
	}
	for _, p := range tests {
		a := old.ToWorld(p)
		b := newXf.ToWorld(p)
		if a.Sub(b).Len() > 1e-6 {
			t.Fatalf("p=%v old=%v new=%v", p, a, b)
		}
	}
}

func TestLocalBoundsCenterBox(t *testing.T) {
	s := &Scene{
		Boxes: []Box{{Min: vec.New(0, 0, 0), Max: vec.New(2, 4, 6)}},
	}
	c, ok := LocalBoundsCenter(s)
	if !ok {
		t.Fatal("expected bounds")
	}
	want := vec.New(1, 2, 3)
	if c.Sub(want).Len() > 1e-9 {
		t.Fatalf("center = %v, want %v", c, want)
	}
}

func TestLocalBoundsCenterEmpty(t *testing.T) {
	_, ok := LocalBoundsCenter(&Scene{})
	if ok {
		t.Fatal("expected false for empty scene")
	}
}

func TestPlacementTransformIdentityNil(t *testing.T) {
	if PlacementTransform(0, 0, 0, vec.V{}, vec.V{}) != nil {
		t.Fatal("expected nil identity")
	}
}

func TestMigratedIncludeAtNoRotation(t *testing.T) {
	oldAt := vec.New(1, 2, 3)
	c := vec.New(0.5, 0.5, 0.5)
	got := MigratedIncludeAt(oldAt, 0, 0, 0, c)
	want := vec.New(1.5, 2.5, 3.5)
	if math.Abs(got.X-want.X) > 1e-9 || math.Abs(got.Y-want.Y) > 1e-9 || math.Abs(got.Z-want.Z) > 1e-9 {
		t.Fatalf("got %v want %v", got, want)
	}
}
