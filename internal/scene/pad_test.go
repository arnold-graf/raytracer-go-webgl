package scene

import "testing"

func TestPadLevelAtOrigin(t *testing.T) {
	s := &Scene{Terrains: []Terrain{{
		Pads: []TerrainPad{{CenterX: 0, CenterZ: 0, HalfX: 8, HalfZ: 8, Level: 0}},
	}}}
	level, ok := s.PadLevelAt(0, 0)
	if !ok || level != 0 {
		t.Fatalf("PadLevelAt(0,0) = (%v,%v), want (0,true)", level, ok)
	}
	if _, ok := s.PadLevelAt(20, 0); ok {
		t.Fatal("point outside pad should miss")
	}
}
