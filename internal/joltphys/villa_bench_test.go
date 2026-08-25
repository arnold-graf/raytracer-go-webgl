package joltphys

import (
	"path/filepath"
	"testing"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestVillaJoltBuildTime(t *testing.T) {
	if testing.Short() {
		t.Skip("jolt mesh build")
	}
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ter := &sc.Terrains[0]
	ter.Prepare()
	gnx, gnz := ter.GridDimensions()
	verts, _ := physicsTerrainMesh(ter)
	t.Logf("render terrain %d×%d → physics mesh %d verts", gnx, gnz, len(verts))

	cfg := camera.Config{EyeHeight: 1.7, CollisionRadius: 0.35, JoltPhysics: true}
	t0 := time.Now()
	w, err := NewWorldFromScene(sc, vec.New(0, 2, 14), cfg)
	build := time.Since(t0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()
	t.Logf("jolt NewWorldFromScene: %v", build)
	if build > 2*time.Second {
		t.Fatalf("jolt build took %v, want <2s (decimated terrain mesh)", build)
	}
}
