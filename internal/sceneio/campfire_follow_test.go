package sceneio

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCampfireLightParams(t *testing.T) {
	dir := t.TempDir()
	fire := filepath.Join(dir, "fire.toml")
	if err := os.WriteFile(fire, []byte(`
[props]
brightness = 10.0
range = 50.0
flicker = 0.45
speed = 1.0

[[light_flickering]]
center = [0.0, 0.0, 0.0]
color = [3.6, 1.7, 0.55]
brightness = 'brightness'
range = 'range'
flicker = 'flicker'
speed = 'speed'
lights = 3
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[include]]
transform_origin = [0, 0, 0]
file = "fire.toml"
at = [0.0, 0.0, 0.0]
params = { brightness = 0.5, range = 30.0, flicker = 0.75, speed = 2.0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Campfires) != 1 {
		t.Fatalf("got %d campfires", len(s.Campfires))
	}
	cf := s.Campfires[0]
	if math.Abs(cf.Brightness-0.5) > 1e-9 {
		t.Fatalf("brightness = %v, want 0.5", cf.Brightness)
	}
	if math.Abs(cf.Range-30.0) > 1e-9 {
		t.Fatalf("range = %v, want 30", cf.Range)
	}
	if math.Abs(cf.Flicker-0.75) > 1e-9 {
		t.Fatalf("flicker = %v, want 0.75", cf.Flicker)
	}
	if math.Abs(cf.Speed-2.0) > 1e-9 {
		t.Fatalf("speed = %v, want 2", cf.Speed)
	}
}

func TestCampfireFollowTerrainViaInclude(t *testing.T) {
	dir := t.TempDir()
	fire := filepath.Join(dir, "fire.toml")
	if err := os.WriteFile(fire, []byte(`
[[light_flickering]]
center = [0.0, 0.5, 0.0]
color = [3.6, 1.7, 0.55]
brightness = 10.0
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(`
[[terrain]]
origin = [-20.0, 0.0, -20.0]
size = [40.0, 40.0]
base = 0.0
detail = 0.0

  [[terrain.feature]]
  kind = "peak"
  pos = [0.0, 0.0]
  height = 8.0
  width = 6.0

[[include]]
transform_origin = [0, 0, 0]
file = "fire.toml"
at = [5.0, 0.0, 0.0]
follow_terrain = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Campfires) != 1 {
		t.Fatalf("got %d campfires", len(s.Campfires))
	}
	cf := s.Campfires[0]
	h, ok := s.TerrainHeightAt(cf.Center.X, cf.Center.Z)
	if !ok {
		t.Fatal("no terrain")
	}
	if math.Abs(cf.Center.Y-h) > 0.6 {
		t.Fatalf("campfire y=%.2f, terrain=%.2f at (%.1f,%.1f)", cf.Center.Y, h, cf.Center.X, cf.Center.Z)
	}
}
