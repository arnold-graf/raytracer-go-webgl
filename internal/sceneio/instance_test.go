package sceneio_test

import (
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestInstancedPineClusterLoad(t *testing.T) {
	root := filepath.Join("..", "..")
	villa := filepath.Join(root, "scenes", "outdoors-night-villa.toml")
	sc, err := sceneio.Load(villa)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.HasInstancing() {
		t.Fatal("expected instancing catalog on villa scene")
	}
	cat := sc.Instancing()
	if len(cat.Templates) != 1 {
		t.Fatalf("templates = %d, want 1 (pine-tree.toml)", len(cat.Templates))
	}
	if len(cat.Placements) != 15 {
		t.Fatalf("placements = %d, want 15 (one pine cluster)", len(cat.Placements))
	}
	if len(sc.Cones) < 15*4 {
		t.Fatalf("materialized cones = %d, want at least %d", len(sc.Cones), 15*4)
	}
}

func TestInstancedMountainsTreeCount(t *testing.T) {
	root := filepath.Join("..", "..")
	mtn := filepath.Join(root, "scenes", "objects", "mountains.toml")
	sc, err := sceneio.Load(mtn)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.HasInstancing() {
		t.Fatal("expected instancing on mountains scene")
	}
	cat := sc.Instancing()
	if len(cat.Templates) != 1 {
		t.Fatalf("templates = %d, want 1", len(cat.Templates))
	}
	if len(cat.Placements) != 60 {
		t.Fatalf("placements = %d, want 60 (4 clusters × 15 trees)", len(cat.Placements))
	}
}
