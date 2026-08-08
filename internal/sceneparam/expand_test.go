package sceneparam_test

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/texture"
)

func repoFile(rel string) string {
	return filepath.Join("..", "..", rel)
}

func TestStaircaseExpand(t *testing.T) {
	stairPath := repoFile("scenes/objects/staircase.toml")
	abs, err := filepath.Abs(stairPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte(
		"[[include]]\nfile = "+strconv.Quote(abs)+"\nat = [0.0, 0.0, 0.0]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := sceneio.Load(parent)
	if err != nil {
		t.Fatalf("load default staircase: %v", err)
	}
	if len(s.Boxes) != 8 {
		t.Fatalf("default steps = %d, want 8", len(s.Boxes))
	}
	last := s.Boxes[7]
	if math.Abs(last.Max.X-4.0) > 1e-9 || math.Abs(last.Max.Y-3.0) > 1e-9 {
		t.Fatalf("last step max = %v, want (4,3,z)", last.Max)
	}

	custom := filepath.Join(dir, "custom.toml")
	if err := os.WriteFile(custom, []byte(
		"[[include]]\nfile = "+strconv.Quote(abs)+"\nat = [0.0, 0.0, 0.0]\nprops = { steps = 4, run = 1.0, rise = 0.5, width = 2.0, texture = \"wood\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := sceneio.Load(custom)
	if err != nil {
		t.Fatalf("load custom staircase: %v", err)
	}
	if len(s2.Boxes) != 4 {
		t.Fatalf("custom steps = %d, want 4", len(s2.Boxes))
	}
	b := s2.Boxes[3]
	if math.Abs(b.Min.X-3.0) > 1e-9 || math.Abs(b.Max.X-4.0) > 1e-9 ||
		math.Abs(b.Max.Y-2.0) > 1e-9 || math.Abs(b.Max.Z-2.0) > 1e-9 {
		t.Fatalf("step 3 bounds min=%v max=%v", b.Min, b.Max)
	}
	if b.Tex != texture.Wood {
		t.Fatalf("step 3 texture = %d, want wood", b.Tex)
	}
}

func TestExpandRejectsGoTemplate(t *testing.T) {
	dir := t.TempDir()
	obj := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(obj, []byte(`
[props]
x = 1
{{$y := 1}}
[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
material = "diffuse"
albedo = [1,1,1]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "scene.toml")
	if err := os.WriteFile(parent, []byte("[[include]]\nfile = \"bad.toml\"\nat = [0,0,0]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := sceneio.Load(parent)
	if err == nil || !strings.Contains(err.Error(), "Go template") {
		t.Fatalf("want Go template rejection, got %v", err)
	}
}
