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
