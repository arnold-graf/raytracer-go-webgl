package scene

import "testing"

func TestHybridCoarseBoundsCoverAnalyticPeak(t *testing.T) {
	ter := Terrain{
		OriginX: -100, OriginZ: -100, SizeX: 200, SizeZ: 200,
		CoarseCell: 3, HybridNearStart: 40, HybridNearEnd: 60,
		Base: 1.2, Detail: 0.26, DetailScale: 0.12,
		Features: []TerrainFeature{{
			PosX: 0, PosZ: 0, Height: 30, Width: 12, Steepness: 2.5,
		}},
	}
	ter.Prepare()
	if !ter.hybridLOD || len(ter.cmin) == 0 {
		t.Fatal("expected hybrid coarse bounds")
	}
	x, z := 0.0, 0.0
	h := ter.heightAnalytic(x, z)
	cx := int((x - ter.OriginX) * ter.cInvDx)
	cz := int((z - ter.OriginZ) * ter.cInvDz)
	if cx < 0 || cz < 0 || cx >= ter.cgnx || cz >= ter.cgnz {
		t.Fatalf("peak outside coarse grid at (%d,%d)", cx, cz)
	}
	idx := cz*ter.cgnx + cx
	if h > ter.cmax[idx] || h < ter.cmin[idx] {
		t.Fatalf("analytic peak %v outside coarse [%v,%v]", h, ter.cmin[idx], ter.cmax[idx])
	}
}
