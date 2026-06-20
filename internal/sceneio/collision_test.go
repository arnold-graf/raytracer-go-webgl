package sceneio

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
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
	feetY, headY := 0.0, 2.0

	// Second villa at (50,0,-10) with rotate_y = -45. Bay front wall center in
	// local space is roughly (0, 3, 7.3).
	xf := scene.NewInstanceTransform(0, -45, 0, vec.New(50, 0, -10))
	face := xf.ToWorld(vec.New(0, 1.5, 7.3))
	if !s.Blocked(face.X, face.Z, feetY, headY, r, step) {
		t.Fatalf("rotated second villa bay front should block at (%.2f, %.2f)", face.X, face.Z)
	}
}
