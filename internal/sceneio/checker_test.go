package sceneio

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestCheckerAlbedo2OnBox(t *testing.T) {
	s, err := Decode([]byte(`
[[box]]
material = "checker"
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 0.1
depth = 1
albedo = [1, 1, 1]
albedo2 = [0.2, 0.3, 0.4]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(s.Boxes))
	}
	bx := s.Boxes[0]
	if bx.Mat != scene.MatChecker {
		t.Fatalf("material = %d, want checker", bx.Mat)
	}
	want := vec.New(0.2, 0.3, 0.4)
	if bx.Albedo2 != want {
		t.Fatalf("albedo2 = %v, want %v", bx.Albedo2, want)
	}
}

func TestCheckerAlbedo2OnPlane(t *testing.T) {
	s, err := Decode([]byte(`
[[plane]]
normal = [0, 1, 0]
d = 0
material = "checker"
albedo = [0.8, 0.8, 0.8]
albedo2 = [0.1, 0.1, 0.1]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Planes) != 1 {
		t.Fatalf("planes = %d, want 1", len(s.Planes))
	}
	pl := s.Planes[0]
	want := vec.New(0.1, 0.1, 0.1)
	if pl.Albedo2 != want {
		t.Fatalf("albedo2 = %v, want %v", pl.Albedo2, want)
	}
}
