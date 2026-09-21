package webgpu

import (
	"path/filepath"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestVillaStaticBLASCoversAllStaticPrims(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	pack, _, _, ok2 := AccelPackForScene(sc)
	if !ok2 {
		t.Fatal("accel pack failed")
	}
	_, _, _, _, _, isp, _, ok := packInstancedScene(sc)
	if !ok {
		t.Fatal("pack failed")
	}
	// The static set runs from 0 up to the first template's base.
	staticN := int(isp.templates[0].PrimBase)
	if len(pack.Geom[0].Leaves) != staticN {
		t.Fatalf("static leaves = %d, want %d static prims", len(pack.Geom[0].Leaves), staticN)
	}
	seen := make([]bool, staticN)
	for _, l := range pack.Geom[0].Leaves {
		if int(l.Prim) >= staticN {
			t.Fatalf("static leaf prim %d out of static range %d", l.Prim, staticN)
		}
		seen[l.Prim] = true
	}
	for i, ok := range seen {
		if !ok {
			t.Fatalf("static prim %d missing from Metal BLAS leaves", i)
		}
	}
}

func TestVillaCenterRayStaticBLASLeafCoversHit(t *testing.T) {
	root := filepath.Join("..", "..")
	sc, err := sceneio.Load(filepath.Join(root, "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	prims, _, nodes, _, _, _, _, ok := packInstancedScene(sc)
	if !ok {
		t.Fatal("pack failed")
	}
	cam := camera.New()
	cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	fwd, right, up := cam.Basis()
	ray := cam.Ray(fwd, right, up, 0, 0, 1024.0/640.0, 1)

	tStatic, idxStatic := gpuBLASNearest(nodes, 0, prims, ray.Origin, ray.Dir, gpuTMiss)
	if tStatic >= gpuTMiss {
		t.Fatal("expected static BVH hit")
	}
	pack, _, _, _ := AccelPackForScene(sc)
	for _, l := range pack.Geom[0].Leaves {
		if l.Prim != idxStatic {
			continue
		}
		if !slabHitLeaf(l.Min, l.Max, ray.Origin, ray.Dir, tStatic) {
			t.Fatalf("leaf for prim %d does not cover hit t=%v", idxStatic, tStatic)
		}
		return
	}
	t.Fatalf("prim %d missing from leaves", idxStatic)
}

func slabHitLeaf(min, max [3]float32, ro, rd vec.V, tMax float64) bool {
	nmin := vec.V{X: float64(min[0]), Y: float64(min[1]), Z: float64(min[2])}
	nmax := vec.V{X: float64(max[0]), Y: float64(max[1]), Z: float64(max[2])}
	return gpuSlabHit(nmin, nmax, ro, rd, tMax)
}
