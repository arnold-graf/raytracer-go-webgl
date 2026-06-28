package npc

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestNPCSpawnIntoScene(t *testing.T) {
	sc := &scene.Scene{}
	sc.NPCSpawns = []scene.NPCSpawn{{
		Rig:  "data/rigs/humanoid.yaml",
		Pose: "idle",
		Pos:  vec.V{},
		Yaw:  0,
	}}
	gen0 := sc.Generation()
	m := NewManager()
	if err := m.Instantiate(sc, FootWorld(sc)); err != nil {
		t.Fatal(err)
	}
	if sc.Generation() <= gen0 {
		t.Fatal("expected Generation bump after Touch")
	}
	if len(sc.DynamicBodies) != 1 {
		t.Fatalf("dynamic bodies = %d, want 1", len(sc.DynamicBodies))
	}
	body := sc.DynamicBodies[0]
	nCyl := body.Cylinders[1] - body.Cylinders[0]
	if nCyl < 6 {
		t.Fatalf("npc cylinders = %d, want at least 6", nCyl)
	}
	if len(m.agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(m.agents))
	}
}

func TestManagerUpdateMovesAgent(t *testing.T) {
	sc := &scene.Scene{}
	sc.NPCSpawns = []scene.NPCSpawn{{
		Rig:     "data/rigs/humanoid.yaml",
		Pose:    "idle",
		Pos:     vec.V{},
		Speed:   1.2,
		Heading: 0,
	}}
	m := NewManager()
	if err := m.Instantiate(sc, FootWorld(sc)); err != nil {
		t.Fatal(err)
	}
	z0 := m.agents[0].Locomotor.HipPos.Z
	gen0 := sc.Generation()
	if !m.Update(sc, FootWorld(sc), 0.1) {
		t.Fatal("expected update to change pose")
	}
	if sc.Generation() <= gen0 {
		t.Fatal("expected Generation bump after locomotion update")
	}
	if m.agents[0].Locomotor.HipPos.Z >= z0 {
		t.Fatalf("hip Z should decrease at heading 0, was %v", z0)
	}
}
