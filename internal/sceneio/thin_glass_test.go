package sceneio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGlassDefaultsThin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pane.toml")
	if err := os.WriteFile(path, []byte(`
[[box]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
width = 2.0
height = 3.0
depth = 0.1
material = "glass"
albedo = [0.9, 0.9, 0.9]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(s.Boxes))
	}
	if !s.Boxes[0].Thin {
		t.Fatal("glass should default to thin")
	}
	if s.Boxes[0].TwoPane {
		t.Fatal("two_pane should default to false")
	}
}

func TestLoadThickGlassOptOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pane.toml")
	if err := os.WriteFile(path, []byte(`
[[box]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
width = 2.0
height = 3.0
depth = 0.1
material = "glass"
thin = false
albedo = [0.9, 0.9, 0.9]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Boxes[0].Thin {
		t.Fatal("thin = false should opt out of thin glass")
	}
}

func TestLoadTwoPaneUsesThickGlass(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pane.toml")
	if err := os.WriteFile(path, []byte(`
[[box]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
width = 2.0
height = 3.0
depth = 0.1
material = "glass"
two_pane = true
albedo = [0.9, 0.9, 0.9]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Boxes[0].TwoPane {
		t.Fatal("two_pane flag not loaded")
	}
	if s.Boxes[0].Thin {
		t.Fatal("two_pane should use thick glass, not thin")
	}
}
