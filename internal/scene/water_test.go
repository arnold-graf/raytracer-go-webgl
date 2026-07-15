package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestWaterInfiniteOceanMasksDryLand(t *testing.T) {
	ter := Terrain{
		OriginX: -50, OriginZ: -50, SizeX: 100, SizeZ: 100,
		Base: 2, Island: TerrainIsland{CenterX: 0, CenterZ: 0, Radius: 20, Margin: 10, Floor: -8},
	}
	ter.Prepare()

	w := WaterPool{Level: 0, Radius: 0, MaskShoreline: true}
	// Offshore: terrain below sea level.
	r := vec.Ray{Origin: vec.V{X: 50, Y: 5, Z: 0}, Dir: vec.V{X: 0, Y: -1, Z: 0}}
	if hit := w.Intersect(r, []Terrain{ter}); math.IsInf(hit, 1) {
		t.Fatal("expected water hit over submerged slope")
	}

	// Summit: dry land above sea level.
	r2 := vec.Ray{Origin: vec.V{X: 0, Y: 8, Z: 0}, Dir: vec.V{X: 0, Y: -1, Z: 0}}
	if hit := w.Intersect(r2, []Terrain{ter}); !math.IsInf(hit, 1) {
		t.Fatalf("expected miss over dry summit, got hit=%v", hit)
	}
}

func TestWaterDiskRadiusStillWorks(t *testing.T) {
	w := WaterPool{CX: 0, CZ: 0, Radius: 5, Level: 0}
	r := vec.Ray{Origin: vec.V{X: 0, Y: 2, Z: 0}, Dir: vec.V{X: 0, Y: -1, Z: 0}}
	if hit := w.Intersect(r, nil); math.Abs(hit-2) > 1e-9 {
		t.Fatalf("hit=%v, want 2", hit)
	}
	r2 := vec.Ray{Origin: vec.V{X: 10, Y: 2, Z: 0}, Dir: vec.V{X: 0, Y: -1, Z: 0}}
	if hit := w.Intersect(r2, nil); !math.IsInf(hit, 1) {
		t.Fatalf("expected miss outside disk, got hit=%v", hit)
	}
}

func TestTerrainIslandPullsEdgesBelowCenter(t *testing.T) {
	ter := Terrain{
		OriginX: -100, OriginZ: -100, SizeX: 200, SizeZ: 200,
		Base: 6,
		Island: TerrainIsland{Radius: 40, Margin: 30, Floor: -10},
	}
	ter.Prepare()
	center := ter.Height(0, 0)
	edge := ter.Height(90, 0)
	if edge >= center {
		t.Fatalf("edge height %.2f should be below center %.2f", edge, center)
	}
	if edge > -5 {
		t.Fatalf("edge height %.2f, want near floor -10", edge)
	}
}
