package sceneio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadThinGlassFlag(t *testing.T) {
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
thin = true
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
		t.Fatal("thin flag not loaded")
	}
}
