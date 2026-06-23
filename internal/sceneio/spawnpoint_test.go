package sceneio

import (
	"math"
	"testing"
)

func TestPlayerSpawnpointInCube(t *testing.T) {
	s, err := Load(repoFile("scenes/manhattan_city_block.toml"))
	if err != nil {
		t.Fatal(err)
	}
	sp, ok := s.Spawnpoint("cube_lab_1")
	if !ok {
		t.Fatal("cube_lab_1 not found after loading manhattan scene")
	}
	if math.Abs(sp.Pos.X-1.5) > 1e-9 || math.Abs(sp.Pos.Z-1.5) > 1e-9 {
		t.Fatalf("pos = %v, want (1.5, _, 1.5)", sp.Pos)
	}
	if !sp.UseFloor || math.Abs(sp.FloorY-0.3) > 1e-9 {
		t.Fatalf("floor = %v useFloor=%v, want 0.3 true", sp.FloorY, sp.UseFloor)
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
