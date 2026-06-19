package scene

import "testing"

func TestTerrainHeightAtNoFootprint(t *testing.T) {
	s := &Scene{Terrains: []Terrain{{Pads: []TerrainPad{{CenterX: 1, CenterZ: 2}}}}}
	if _, ok := s.TerrainHeightAt(0, 0); ok {
		t.Fatal("stub terrain without size should not drive placement")
	}
}

func TestTerrainHeightAtPeak(t *testing.T) {
	ter := Terrain{
		OriginX: -10, OriginZ: -10, SizeX: 20, SizeZ: 20, Base: 0,
		Features: []TerrainFeature{{PosX: 0, PosZ: 0, Height: 8, Width: 4}},
	}
	ter.Prepare()
	s := &Scene{Terrains: []Terrain{ter}}
	h, ok := s.TerrainHeightAt(0, 0)
	if !ok {
		t.Fatal("expected height field")
	}
	if h < 7.5 || h > 8.5 {
		t.Fatalf("peak height = %v, want ~8", h)
	}
}
