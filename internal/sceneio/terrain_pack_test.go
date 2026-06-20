package sceneio

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/webgpu"
)

func TestVilla400TerrainPacksForGPU(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate enlarging the map to 400×400 m (same features, bigger footprint).
	if len(s.Terrains) == 0 {
		t.Fatal("expected terrain")
	}
	ter := &s.Terrains[0]
	ter.OriginX, ter.OriginZ = -80, -80
	ter.SizeX, ter.SizeZ = 400, 400
	ter.Prepare()
	gnx, gnz := ter.GridDimensions()

	terrains, samples := webgpu.PackTerrains(s)
	if len(terrains) != 1 {
		t.Fatalf("got %d terrains, want 1", len(terrains))
	}
	want := gnx * gnz
	if len(samples)/4 != want {
		t.Fatalf("packed %d samples, want %d (grid %d×%d)", len(samples)/4, want, gnx, gnz)
	}
	if want > scene.MaxTerrainGridCells {
		t.Fatalf("grid %d×%d exceeds GPU cap", gnx, gnz)
	}
}
