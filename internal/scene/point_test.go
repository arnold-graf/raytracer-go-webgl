package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestPointPlaced(t *testing.T) {
	p := Point{
		ID: "lab", Pos: vec.New(1, 0, 2), Yaw: 0, UseFloor: true, FloorY: 0.3,
	}
	xf := NewInstanceTransform(0, 90, 0, vec.New(10, 0, 0))
	got := p.Placed(xf)
	want := xf.ToWorld(vec.New(1, 0, 2))
	if math.Abs(got.Pos.X-want.X) > 1e-9 || math.Abs(got.Pos.Z-want.Z) > 1e-9 {
		t.Fatalf("pos = %v, want %v", got.Pos, want)
	}
	wantFloor := xf.ToWorld(vec.New(1, 0.3, 2)).Y
	if math.Abs(got.FloorY-wantFloor) > 1e-9 {
		t.Fatalf("floor_y = %v, want %v", got.FloorY, wantFloor)
	}
	if math.Abs(got.Yaw+math.Pi/2) > 1e-6 {
		t.Fatalf("yaw = %v, want -pi/2", got.Yaw)
	}
}

func TestScenePointByID(t *testing.T) {
	s := &Scene{Points: []Point{{ID: "a"}, {ID: "b"}}}
	if _, ok := s.PointByID("c"); ok {
		t.Fatal("expected miss")
	}
	if p, ok := s.PointByID("b"); !ok || p.ID != "b" {
		t.Fatalf("lookup b: ok=%v p=%+v", ok, p)
	}
}
