package sceneio

import (
	"math"
	"testing"
)

func TestPointInCube(t *testing.T) {
	s, err := Load(repoFile("scenes/office-sunset/index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s.PointByID("cube_lab_1")
	if !ok {
		t.Fatal("cube_lab_1 not found after loading office-sunset scene")
	}
	// Visual parity with origin-at authoring: index [0,200,0] + cube [10,1,2] + spawn (1.5,0,1.5).
	wantX := 10.0 + 1.5
	wantZ := 2.0 + 1.5
	if math.Abs(p.Pos.X-wantX) > 0.05 || math.Abs(p.Pos.Z-wantZ) > 0.3 {
		t.Fatalf("pos = %v, want x≈%v z≈%v", p.Pos, wantX, wantZ)
	}
	if math.Abs(p.Pos.Y-201.0) > 2.0 {
		t.Fatalf("pos Y = %v, want ~201", p.Pos.Y)
	}
	if !p.UseFloor || math.Abs(p.FloorY-201.3) > 2.0 {
		t.Fatalf("floor = %v useFloor=%v, want ~201.3 true", p.FloorY, p.UseFloor)
	}
}

func TestPointDuplicateID(t *testing.T) {
	_, err := Decode([]byte(`
[[point]]
id = "a"
pos = [0, 0, 0]

[[point]]
id = "a"
pos = [1, 0, 0]
`))
	if err == nil {
		t.Fatal("expected duplicate id error")
	}
}
