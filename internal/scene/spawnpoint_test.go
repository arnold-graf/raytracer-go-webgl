package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestPlayerSpawnpointPlaced(t *testing.T) {
	sp := PlayerSpawnpoint{
		ID: "lab", Pos: vec.New(1, 0, 2), Yaw: 0, UseFloor: true, FloorY: 0.3,
	}
	xf := NewInstanceTransform(0, 90, 0, vec.New(10, 0, 0))
	got := sp.Placed(xf)
	want := xf.ToWorld(vec.New(1, 0, 2))
	if math.Abs(got.Pos.X-want.X) > 1e-9 || math.Abs(got.Pos.Z-want.Z) > 1e-9 {
		t.Fatalf("pos = %v, want %v", got.Pos, want)
	}
	if math.Abs(got.Yaw+math.Pi/2) > 1e-6 {
		t.Fatalf("yaw = %v, want -pi/2", got.Yaw)
	}
}

func TestSceneSpawnpointLookup(t *testing.T) {
	s := &Scene{Spawnpoints: []PlayerSpawnpoint{{ID: "a"}, {ID: "b"}}}
	if _, ok := s.Spawnpoint("c"); ok {
		t.Fatal("expected miss")
	}
	if sp, ok := s.Spawnpoint("b"); !ok || sp.ID != "b" {
		t.Fatalf("lookup b: ok=%v sp=%+v", ok, sp)
	}
}
