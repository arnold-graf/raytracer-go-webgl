package sceneio

import (
	"math"
	"testing"
)

func TestPlayerSpawnpointInCube(t *testing.T) {
	s, err := Load(repoFile("scenes/office-sunset/index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := s.Spawnpoint("cube_lab_1")
	if !ok {
		t.Fatal("cube_lab_1 not found after loading office-sunset scene")
	}
	wantX := 10.0 + 1.5
	wantZ := 2.0 + 1.5
	if math.Abs(sp.Pos.X-wantX) > 1e-9 || math.Abs(sp.Pos.Z-wantZ) > 1e-9 {
		t.Fatalf("pos = %v, want x=%v z=%v", sp.Pos, wantX, wantZ)
	}
	if !sp.UseFloor || math.Abs(sp.FloorY-(200.0+1.0+0.3)) > 1e-9 {
		t.Fatalf("floor = %v useFloor=%v, want 201.3 true", sp.FloorY, sp.UseFloor)
	}
}

func TestPlayerSpawnpointDuplicateID(t *testing.T) {
	_, err := Decode([]byte(`
[[player_spawnpoint]]
id = "a"
pos = [0, 0, 0]

[[player_spawnpoint]]
id = "a"
pos = [1, 0, 0]
`))
	if err == nil {
		t.Fatal("expected duplicate id error")
	}
}
