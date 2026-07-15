package scene

import (
	"math"
	"testing"
)

func TestTerrainHybridCoarseBakeOmitsFBm(t *testing.T) {
	ter := Terrain{
		OriginX: -50, OriginZ: -50, SizeX: 100, SizeZ: 100,
		Base: 0, Detail: 0.35, DetailScale: 0.12,
		CoarseCell: 4,
		Features: []TerrainFeature{
			{PosX: 0, PosZ: 0, Height: 8, Width: 10, Steepness: 2},
		},
	}
	ter.Prepare()
	if !ter.HybridLOD() {
		t.Fatal("expected hybrid LOD with coarse_cell set")
	}
	gnx, gnz := ter.GridDimensions()
	if gnx*gnz > MaxTerrainGridCells {
		t.Fatalf("grid %d×%d exceeds cap", gnx, gnz)
	}
	dx := ter.SizeX / float64(gnx-1)
	for j := 0; j < gnz; j++ {
		z := ter.OriginZ + float64(j)*dx
		for i := 0; i < gnx; i++ {
			x := ter.OriginX + float64(i)*dx
			got := ter.Height(x, z)
			coarse := ter.heightCoarseAnalytic(x, z)
			baked := ter.hgrid[j*gnx+i]
			if diff := math.Abs(baked - coarse); diff > 1e-9 {
				t.Fatalf("vertex (%d,%d): baked=%v coarse=%v", i, j, baked, coarse)
			}
			if diff := math.Abs(got - ter.heightAnalytic(x, z)); diff > 1e-9 {
				t.Fatalf("Height() should use full analytic in hybrid mode")
			}
		}
	}
}
