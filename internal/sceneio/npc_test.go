package sceneio

import (
	"testing"
)

func TestNPCTestSceneLoads(t *testing.T) {
	s, err := Load(repoFile("scenes/npc-test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.NPCSpawns) != 1 {
		t.Fatalf("npc spawns = %d, want 1", len(s.NPCSpawns))
	}
	if s.NPCSpawns[0].Rig != "data/rigs/humanoid.yaml" {
		t.Fatalf("rig = %q", s.NPCSpawns[0].Rig)
	}
	if len(s.NPCSpawns[0].Patrol) != 3 {
		t.Fatalf("patrol waypoints = %d, want 3", len(s.NPCSpawns[0].Patrol))
	}
}

func TestNPCSpiderTestSceneLoads(t *testing.T) {
	s, err := Load(repoFile("scenes/npc-spider-test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.NPCSpawns) != 1 {
		t.Fatalf("npc spawns = %d, want 1", len(s.NPCSpawns))
	}
	if s.NPCSpawns[0].Rig != "data/rigs/spider.yaml" {
		t.Fatalf("rig = %q", s.NPCSpawns[0].Rig)
	}
}

func TestNPCPatrolAndGoalConflict(t *testing.T) {
	_, err := Decode([]byte(`
[[npc]]
rig = "data/rigs/humanoid.yaml"
at = [0.0, 0.0, 0.0]
patrol = [[0.0, 0.0, 0.0], [1.0, 0.0, 0.0]]
goal = [2.0, 0.0, 0.0]
`))
	if err == nil {
		t.Fatal("expected patrol+goal conflict error")
	}
}

func TestNPCPatrolWithHeightHint(t *testing.T) {
	s, err := Decode([]byte(`
[[npc]]
rig = "data/rigs/humanoid.yaml"
at = [5.0, 1.0, 8.0]
patrol = [[8.0, 0.0, 10.0], [20.0, 0.2, 20.0], [8.0, 0.0, 10.0]]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.NPCSpawns) != 1 {
		t.Fatalf("npc spawns = %d, want 1", len(s.NPCSpawns))
	}
	sp := s.NPCSpawns[0]
	if sp.Pos.Y != 1.0 {
		t.Fatalf("spawn Y = %v, want 1.0", sp.Pos.Y)
	}
	if len(sp.Patrol) != 3 || sp.Patrol[1].Y != 0.2 {
		t.Fatalf("patrol = %v", sp.Patrol)
	}
}

func TestNPCMissingRig(t *testing.T) {
	_, err := Decode([]byte(`
[[npc]]
pose = "idle"
at = [0.0, 0.0, 0.0]
`))
	if err == nil {
		t.Fatal("expected missing rig error")
	}
}
