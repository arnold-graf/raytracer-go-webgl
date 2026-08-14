package sceneio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInteractiveLightLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lamp.toml")
	if err := os.WriteFile(path, []byte(`
[[light]]
pos = [0.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]
interactive = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Lights) != 1 {
		t.Fatalf("lights = %d", len(s.Lights))
	}
	l := s.Lights[0]
	if !l.Interactive {
		t.Fatal("expected interactive")
	}
	if l.Hint != "lamp" {
		t.Fatalf("hint = %q, want lamp", l.Hint)
	}
}

func TestInteractiveLightCustomHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lamp.toml")
	if err := os.WriteFile(path, []byte(`
[[light]]
pos = [0.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]
interactive = true
hint = "overhead"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Lights[0].Hint != "overhead" {
		t.Fatalf("hint = %q", s.Lights[0].Hint)
	}
}
