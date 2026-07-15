package sceneio

import (
	"math"
	"testing"
)

func TestOutdoorVillaPadFlattensToGrade(t *testing.T) {
	s, err := Load(repoFile("scenes/outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	x, z := 0.0, -8.0
	ter, _ := s.TerrainHeightAt(x, z)
	if ter < 2.5 || ter > 3.5 {
		t.Fatalf("baked at villa (0,-8) = %.2f, want ~3 (absolute pad)", ter)
	}
	var plinthY float64
	for _, b := range s.Boxes {
		mn, mx := b.WorldBounds()
		cx, cz := (mn.X+mx.X)/2, (mn.Z+mx.Z)/2
		if math.Abs(cx) < 5 && cz < -5 && cz > -11 && mx.Y-mn.Y < 1.5 {
			plinthY = mn.Y
			break
		}
	}
	if plinthY < 2.5 || plinthY > 3.5 {
		t.Fatalf("plinth y = %.2f, want ~3 on flat pad", plinthY)
	}
}
