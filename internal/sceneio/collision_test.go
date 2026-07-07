package sceneio

import (
	"testing"
)

func TestVillaCenterPathNotBlockedByPhantomTrees(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	feetY, headY, r, step := 0.0, 2.0, 0.30, 0.45
	if s.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatal("world origin should not be blocked by mis-placed tree trunks")
	}
}

func TestPineTreeBlocksAtPlacedLocation(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	const r, step = 0.30, 0.45
	feetY := s.GroundHeight(-18, -6, 2.0)
	headY := feetY + 2.0
	if !s.Blocked(-18, -6, feetY, headY, r, step) {
		t.Fatal("pine trunk should block walking through the tree base")
	}
}

func TestSecondVillaRotatedWallBlocks(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	const r, step = 0.30, 0.45
	feetY := s.GroundHeight(50, -10, 2.0)
	headY := feetY + 2.0
	// Second villa (rotate_y = -45) should present solid facade geometry near x≈50.
	blocked := 0
	for i := range s.Boxes {
		mn, mx := s.Boxes[i].WorldBounds()
		if mx.X < 38 || mn.X > 58 || mn.Z > 2 {
			continue
		}
		midX := (mn.X + mx.X) / 2
		midZ := (mn.Z + mx.Z) / 2
		if s.Blocked(midX, midZ, feetY, headY, r, step) {
			blocked++
		}
	}
	if blocked < 3 {
		t.Fatalf("expected several blocking facade samples on second villa, got %d", blocked)
	}
}
