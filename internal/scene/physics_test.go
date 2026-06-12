package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

// buildingScene mimics the indoor-outdoor layout: terrain with a hill, a raised
// stone foundation, and a tall wall with a door gap.
func buildingScene() *Scene {
	ter := Terrain{
		OriginX: -40, OriginZ: -40, SizeX: 80, SizeZ: 80,
		Base: 0, Detail: 0, DetailScale: 0.1, Step: 0.3,
		Features: []TerrainFeature{
			{PosX: -20, PosZ: 0, Height: 5, Width: 8, Steepness: 2, ExtendX: 1, ExtendZ: 1},
		},
	}
	ter.Prepare()

	return &Scene{
		Terrains: []Terrain{ter},
		Boxes: []Box{
			// Foundation / floor slab, top at y=0.4 over x[10,20], z[-8,4].
			{Min: vec.New(10, -0.3, -8), Max: vec.New(20, 0.4, 4)},
			// South wall, left of the door (door gap x[14.5,16.5]).
			{Min: vec.New(10, 0, 3.5), Max: vec.New(14.5, 6.3, 3.9)},
			// South wall, right of the door.
			{Min: vec.New(16.5, 0, 3.5), Max: vec.New(20, 6.3, 3.9)},
		},
	}
}

func TestGroundHeightFoundationOverridesTerrain(t *testing.T) {
	s := buildingScene()
	if g := s.GroundHeight(15, 0, 2.0); math.Abs(g-0.4) > 1e-9 {
		t.Fatalf("ground inside building = %v, want 0.4 (foundation)", g)
	}
}

func TestGroundHeightFollowsTerrain(t *testing.T) {
	s := buildingScene()
	peak := s.GroundHeight(-20, 0, 100)
	if peak < 4.5 {
		t.Fatalf("ground at hilltop = %v, want ~5", peak)
	}
	flat := s.GroundHeight(0, 20, 100)
	if math.Abs(flat) > 0.2 {
		t.Fatalf("ground on flat terrain = %v, want ~0", flat)
	}
	if peak <= flat {
		t.Fatalf("terrain not followed: peak %v should exceed flat %v", peak, flat)
	}
}

// TestBlockedHonorsBoxTransform guards against the regression where collision
// used a box's local Min/Max and ignored its world transform, producing a
// phantom wall at the origin and none at the real (translated) location.
func TestBlockedHonorsBoxTransform(t *testing.T) {
	xf := NewInstanceTransform(0, 0, 0, vec.New(16, 0, -2))
	s := &Scene{Boxes: []Box{
		// A wall authored in local space (around the origin), placed at world
		// (16,0,-2) via the include transform.
		{Min: vec.New(-1, 0, -1), Max: vec.New(1, 6, 1), Surface: Surface{Xform: xf}},
	}}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45

	// The wall is really at world (16,0,-2); local coords would have it at origin.
	if !s.Blocked(16, -2, feetY, headY, r, step) {
		t.Fatalf("expected the wall to block at its world position (16,-2)")
	}
	if s.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatalf("origin must be clear (no phantom wall from local coords)")
	}
}

// TestSlantedRoofDoesNotBlock checks hypothesis B: a high tilted roof box stays
// above the player's head in world space and never blocks walking.
func TestSlantedRoofDoesNotBlock(t *testing.T) {
	roof := NewTransform(-12, 0, 0, vec.New(0, 5.9, 0))
	place := NewInstanceTransform(0, 0, 0, vec.New(16, 0, -2))
	s := &Scene{Boxes: []Box{
		{Min: vec.New(-4.7, 5.8, -5.7), Max: vec.New(4.7, 6.1, 5.7),
			Surface: Surface{Xform: place.Compose(roof)}},
	}}
	if s.Blocked(16, -2, 0.0, 2.0, 0.3, 0.45) {
		t.Fatalf("slanted roof (overhead) should not block a walking player")
	}
}

func TestBlockedAtWallButNotDoorOrFloor(t *testing.T) {
	s := buildingScene()
	feetY, headY, r, step := 0.4, 2.0, 0.3, 0.45

	if !s.Blocked(12, 3.7, feetY, headY, r, step) {
		t.Fatalf("expected the south wall to block movement")
	}
	if s.Blocked(15.5, 3.7, feetY, headY, r, step) {
		t.Fatalf("door gap should be passable")
	}
	if s.Blocked(15, 0, feetY, headY, r, step) {
		t.Fatalf("standing on the foundation should not be blocked")
	}
}
