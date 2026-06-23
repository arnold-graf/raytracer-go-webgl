package sceneio

import (
	"testing"
)

func TestMountainsCampfireTerrain(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for i, cf := range s.Campfires {
		h, ok := s.TerrainHeightAt(cf.Center.X, cf.Center.Z)
		if !ok {
			continue
		}
		delta := cf.Center.Y - h
		t.Logf("campfire[%d] center=(%.1f,%.1f,%.1f) terrain=%.2f delta=%.2f", i, cf.Center.X, cf.Center.Y, cf.Center.Z, h, delta)
		// mountains campfire: local at [16, 0.5, 24] with follow_terrain
		if cf.Center.X > 10 && cf.Center.X < 22 && cf.Center.Z > 220 {
			if delta < 0.2 || delta > 1.0 {
				t.Fatalf("mountains campfire delta=%.2f, want ~0.5 above terrain", delta)
			}
		}
	}
}
