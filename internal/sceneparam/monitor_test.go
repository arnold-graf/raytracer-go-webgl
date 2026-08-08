package sceneparam_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"raytracer/internal/sceneio"
)

func TestLoadWorkstationMonitorWithParams(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "office-sunset", "objects", "workstation-monitor.toml")
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(
		"[[include]]\nfile = "+strconv.Quote(abs)+"\nat = [0,0,0]\nprops = { screen_id = \"monitor_2\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := sceneio.Load(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ScreenSpecs) != 1 || s.ScreenSpecs[0].ID != "monitor_2" {
		t.Fatalf("screen = %+v", s.ScreenSpecs)
	}
}
