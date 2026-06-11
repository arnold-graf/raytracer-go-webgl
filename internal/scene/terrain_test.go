package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestTerrainCoarseMarchMatchesFineMarch(t *testing.T) {
	ter := Terrain{
		OriginX: -40, OriginZ: -40,
		SizeX: 80, SizeZ: 80,
		Base: 0, Detail: 0.35, DetailScale: 0.12, Step: 0.28,
		Features: []TerrainFeature{
			{PosX: -16, PosZ: -24, Height: 12, Width: 11, Steepness: 2, ExtendX: 1, ExtendZ: 1},
			{PosX: 15, PosZ: -26, Height: 10, Width: 11, Steepness: 2, ExtendX: 1, ExtendZ: 1},
			{PosX: 0, PosZ: -33, Height: 9, Width: 24, Steepness: 2, ExtendX: 3, ExtendZ: 1},
			{PosX: -24, PosZ: -8, Height: 3.5, Width: 13, Steepness: 2, ExtendX: 1, ExtendZ: 1},
			{PosX: 0, PosZ: 8, Height: -4, Width: 6, Steepness: 3, ExtendX: 1, ExtendZ: 1},
		},
	}
	ter.Prepare()

	origin := vec.V{X: 0, Y: 7, Z: 22}
	for iy := 0; iy < 12; iy++ {
		for ix := 0; ix < 16; ix++ {
			x := (float64(ix)/15 - 0.5) * 1.3
			y := -0.15 - float64(iy)/11*0.7
			r := vec.Ray{Origin: origin, Dir: vec.V{X: x, Y: y, Z: -1}.Normalize()}

			tEnter, tExit, ok := ter.slab(r)
			if !ok {
				continue
			}
			if tEnter < eps {
				tEnter = eps
			}
			got := ter.marchCoarse(r, tEnter, tExit, true)
			want := ter.marchFine(r, tEnter, tExit, true)
			if math.IsInf(got, 1) || math.IsInf(want, 1) {
				if math.IsInf(got, 1) != math.IsInf(want, 1) {
					t.Fatalf("ray (%d,%d): coarse=%v fine=%v", ix, iy, got, want)
				}
				continue
			}
			if diff := math.Abs(got - want); diff > 1e-3 {
				t.Fatalf("ray (%d,%d): coarse=%v fine=%v diff=%v", ix, iy, got, want, diff)
			}
		}
	}
}
