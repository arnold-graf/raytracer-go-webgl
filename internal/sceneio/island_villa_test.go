package sceneio

import (
	"math"
	"testing"
)

func TestIslandVillaPadDiagnostics(t *testing.T) {
	s, err := Load(repoFile("scenes/island.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range s.Terrains[0].Pads {
		nat, _ := s.NaturalTerrainHeightAt(p.CenterX, p.CenterZ)
		baked := s.Terrains[0].Height(p.CenterX, p.CenterZ)
		t.Logf("pad[%d] center=(%.2f,%.2f) level=%.2f natural=%.2f baked=%.2f abs=%v",
			i, p.CenterX, p.CenterZ, p.Level, nat, baked, p.Absolute)
	}
	var plinthY float64
	for i, b := range s.Boxes {
		mn, mx := b.WorldBounds()
		if mx.Y-mn.Y > 0.5 && mx.Y < 15 && mn.Y < 20 {
			if plinthY == 0 || mn.Y < plinthY {
				plinthY = mn.Y
			}
			if i < 3 {
				t.Logf("box[%d] y=[%.2f, %.2f]", i, mn.Y, mx.Y)
			}
		}
	}
	t.Logf("lowest villa-ish box min y = %.2f", plinthY)
	if xf := s.Boxes[0].Xform; xf != nil {
		t.Logf("box0 anchor world = %+v", xf.PlacementAnchor())
	}
}

func TestIslandVillaPlinthNearPad(t *testing.T) {
	s, err := Load(repoFile("scenes/island.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var padLevel float64
	var padX, padZ float64
	for _, p := range s.Terrains[0].Pads {
		if math.Abs(p.HalfX-8.5) < 0.1 {
			padLevel, padX, padZ = p.Level, p.CenterX, p.CenterZ
		}
	}
	if padLevel == 0 {
		t.Fatalf("no villa pad found: %+v", s.Terrains[0].Pads)
	}
	var plinthY float64
	for _, b := range s.Boxes {
		mn, mx := b.WorldBounds()
		if mn.Y > 0 && mx.Y-mn.Y >= 1.0 && mx.Y-mn.Y <= 1.5 {
			plinthY = mn.Y
			break
		}
	}
	t.Logf("pad=(%.1f,%.1f) level=%.2f plinth=%.2f delta=%.2f", padX, padZ, padLevel, plinthY, plinthY-padLevel)
	if math.Abs(plinthY-padLevel) > 0.5 {
		t.Fatalf("plinth y=%.2f too far from pad level %.2f", plinthY, padLevel)
	}
}
