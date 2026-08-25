package joltphys

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/sceneio"
)

func TestPhysicsTerrainRegionIsland(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "island.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ter := &sc.Terrains[0]
	x0, z0, x1, z1 := physicsTerrainRegion(ter)
	sx, sz := x1-x0, z1-z0
	if sx > 850 || sz > 850 {
		t.Fatalf("island physics region %.0f×%.0f m, want landmass clip not full 4000 m", sx, sz)
	}
	if sx < 700 || sz < 700 {
		t.Fatalf("island physics region %.0f×%.0f m too small", sx, sz)
	}
}

func TestPhysicsTerrainMeshUsesHeight(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "island.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ter := &sc.Terrains[0]
	ter.Prepare()
	verts, _ := physicsTerrainMesh(ter)
	if len(verts) == 0 {
		t.Fatal("expected physics mesh")
	}
	for _, v := range verts {
		want := ter.Height(float64(v.X), float64(v.Z))
		if math.Abs(float64(v.Y)-want) > 1e-3 {
			t.Fatalf("vertex (%.1f,%.1f): mesh y=%.3f Height()=%.3f", v.X, v.Z, v.Y, want)
		}
	}
}

func TestIslandSpawnAbovePhysicsTerrain(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "island.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ter := &sc.Terrains[0]
	ter.Prepare()
	verts, indices := physicsTerrainMesh(ter)

	px, pz := 0.0, 144.0
	ground := ter.Height(px, pz)
	t.Logf("ground at spawn (%.0f,%.0f): %.2f, physics verts=%d tris=%d",
		px, pz, ground, len(verts), len(indices)/3)

	// Sample triangle planes near spawn; mesh should be within ~1 m of analytic ground.
	minY := math.Inf(1)
	for j := 0; j < len(indices); j += 3 {
		v0, v1, v2 := verts[indices[j]], verts[indices[j+1]], verts[indices[j+2]]
		for _, v := range [3]jolt.Vec3{v0, v1, v2} {
			d := math.Hypot(float64(v.X)-px, float64(v.Z)-pz)
			if d < 8 {
				y := triangleYAt(v0, v1, v2, px, pz)
				if y < minY {
					minY = y
				}
			}
		}
	}
	if minY < ground-1.5 {
		t.Fatalf("physics mesh near spawn min y=%.2f, ground=%.2f (gap %.2f)", minY, ground, ground-minY)
	}
}

// triangleYAt returns the Y of the triangle plane at (x,z), or NaN if outside.
func triangleYAt(v0, v1, v2 jolt.Vec3, x, z float64) float64 {
	// Barycentric test in XZ; if inside, interpolate Y.
	ax, az := float64(v0.X), float64(v0.Z)
	bx, bz := float64(v1.X), float64(v1.Z)
	cx, cz := float64(v2.X), float64(v2.Z)
	denom := (bz-cz)*(ax-cx) + (cx-bx)*(az-cz)
	if math.Abs(denom) < 1e-9 {
		return math.NaN()
	}
	w0 := ((bz-cz)*(x-cx) + (cx-bx)*(z-cz)) / denom
	w1 := ((cz-az)*(x-cx) + (ax-cx)*(z-cz)) / denom
	w2 := 1 - w0 - w1
	if w0 < -0.01 || w1 < -0.01 || w2 < -0.01 {
		return math.NaN()
	}
	return w0*float64(v0.Y) + w1*float64(v1.Y) + w2*float64(v2.Y)
}
