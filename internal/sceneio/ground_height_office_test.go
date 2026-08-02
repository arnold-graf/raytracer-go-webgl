package sceneio

import (
	"testing"
)

// Player reported standing on invisible ground at HUD [45.7, 11.6, 202.1]
// (world x=45.7, z=11.6, feet y≈202.1). That was phantom cushion height from
// rotated comfy-couch boxes, not a real collider at that spot.
func TestOfficeSunsetNoPhantomGroundAtReportedPosition(t *testing.T) {
	s, err := Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	const (
		worldX = 45.7
		worldZ = 11.6
		headY  = 202.5
	)
	g := s.GroundHeight(worldX, worldZ, headY)
	// Floor slab is at y=200; allow small tolerance above that.
	if g > 200.05 {
		t.Fatalf("GroundHeight(%v, %v) = %v, want floor ~200 (phantom cushion)", worldX, worldZ, g)
	}
}
