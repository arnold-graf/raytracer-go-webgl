package scene

import "testing"

func TestResolveRelativePadsInvalidatesBake(t *testing.T) {
	s := &Scene{Terrains: []Terrain{{
		OriginX: -50, OriginZ: -50, SizeX: 100, SizeZ: 100,
		Base: 0, Detail: 0,
		Features: []TerrainFeature{{
			PosX: 0, PosZ: 0, Height: 20, Width: 12, Steepness: 2,
		}},
	}}}
	// Simulate an early bake with an unresolved relative pad (level still 3).
	s.Terrains[0].Pads = []TerrainPad{{
		CenterX: 0, CenterZ: 0, HalfX: 8, HalfZ: 8, Level: 3, Margin: 2,
	}}
	s.Terrains[0].Prepare()
	if h := s.Terrains[0].Height(0, 0); h > 5 {
		t.Fatalf("premature bake height = %v, want ~3", h)
	}

	s.Terrains[0].Pads[0].Absolute = false
	s.PrepareTerrains()
	if h := s.Terrains[0].Height(0, 0); h < 22 {
		t.Fatalf("after resolve bake height = %v, want ~23", h)
	}
}
