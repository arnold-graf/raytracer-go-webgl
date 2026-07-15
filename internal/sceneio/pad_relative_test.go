package sceneio_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestRelativePadOnMountainFlattensToNaturalPlusOffset(t *testing.T) {
	dir := t.TempDir()
	villa := filepath.Join(dir, "site.toml")
	if err := os.WriteFile(villa, []byte(`
[[terrain]]
[[terrain.pad]]
center = [0.0, 0.0]
half = [8.0, 8.0]
level = 3.0
margin = 4.0

[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[terrain]]
origin = [-100.0, 0.0, -100.0]
size = [200.0, 200.0]
base = 0.0
detail = 0.0

  [[terrain.feature]]
  kind = "peak"
  pos = [2.0, -18.0]
  height = 18.0
  width = 14.0
  steepness = 2.0

[[include]]
transform_origin = [0, 0, 0]
file = "site.toml"
at = [2.0, 0.0, -18.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := sceneio.Load(parent)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	natural, ok := s.NaturalTerrainHeightAt(2, -18)
	if !ok {
		t.Fatal("expected natural terrain at villa site")
	}
	var padLevel float64
	for _, p := range s.Terrains[0].Pads {
		if math.Abs(p.CenterX-2) < 0.01 && math.Abs(p.CenterZ+18) < 0.01 {
			padLevel = p.Level
		}
	}
	if padLevel < natural+2.5 || padLevel > natural+3.5 {
		t.Fatalf("pad level = %v, want natural(%v)+3", padLevel, natural)
	}
	mn, _ := s.Boxes[0].WorldBounds()
	if mn.Y < natural+2.5 || mn.Y > natural+3.5 {
		t.Fatalf("plinth y = %v, want ~%v", mn.Y, natural+3)
	}
}
