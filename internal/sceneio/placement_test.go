package sceneio

import (
	"math"
	"strings"
	"testing"
)

func TestPlacementCenterBox(t *testing.T) {
	s, err := Decode([]byte(`
[[box]]
center_x = 1.0
center_y = 0.5
center_z = -2.0
width = 2.0
height = 1.0
depth = 4.0
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`))
	if err != nil {
		t.Fatal(err)
	}
	b := s.Boxes[0]
	if math.Abs(b.Min.X) > 1e-9 || math.Abs(b.Min.Y) > 1e-9 || math.Abs(b.Min.Z+4) > 1e-9 {
		t.Fatalf("min = %v, want (0,0,-4)", b.Min)
	}
	if math.Abs(b.Max.X-2) > 1e-9 || math.Abs(b.Max.Y-1) > 1e-9 || math.Abs(b.Max.Z) > 1e-9 {
		t.Fatalf("max = %v, want (2,1,0)", b.Max)
	}
}

func TestPlacementMixedAxesCylinder(t *testing.T) {
	s, err := Decode([]byte(`
[[cylinder]]
pos_x = -0.1
center_y = 2.0
center_z = 0.0
width = 0.2
height = 4.0
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`))
	if err != nil {
		t.Fatal(err)
	}
	c := s.Cylinders[0]
	if math.Abs(c.CX) > 1e-9 || math.Abs(c.CZ) > 1e-9 {
		t.Fatalf("footprint center = (%v,%v), want (0,0)", c.CX, c.CZ)
	}
	if math.Abs(c.YMin) > 1e-9 || math.Abs(c.YMax-4) > 1e-9 {
		t.Fatalf("y = [%v,%v], want [0,4]", c.YMin, c.YMax)
	}
}

func TestPlacementExclusiveAxisRejected(t *testing.T) {
	_, err := Decode([]byte(`
[[box]]
pos_x = 0.0
center_x = 1.0
width = 1.0
height = 1.0
depth = 1.0
material = "diffuse"
albedo = [1.0, 1.0, 1.0]
`))
	if err == nil || !strings.Contains(err.Error(), "pos_x and center_x") {
		t.Fatalf("expected exclusive axis error, got %v", err)
	}
}
