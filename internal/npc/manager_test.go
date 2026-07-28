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
	z0 := m.agents[0].LocomotorState().HipPos.Z
	gen0 := sc.Generation()
	x0 := sc.TransformGeneration()
	if !m.Update(sc, FootWorld(sc), 0.1) {
		t.Fatal("expected update to change pose")
	}
	if sc.Generation() != gen0 {
		t.Fatal("locomotion update should use TouchTransforms, not Touch")
	}
	if sc.TransformGeneration() <= x0 {
		t.Fatal("expected TransformGeneration bump after locomotion update")
	}
	if m.agents[0].LocomotorState().HipPos.Z >= z0 {
		t.Fatalf("hip Z should decrease at heading 0, was %v", z0)
	}
}

func BenchmarkManagerUpdate10Agents(b *testing.B) {
	sc := &scene.Scene{}
	for i := 0; i < 10; i++ {
		sc.NPCSpawns = append(sc.NPCSpawns, scene.NPCSpawn{
			Rig:     "data/rigs/humanoid.yaml",
			Pose:    "idle",
			Pos:     vec.V{X: float64(i) * 0.5},
			Speed:   1.2,
			Heading: 270,
		})
	}
	m := NewManager()
	if err := m.Instantiate(sc, FootWorld(sc)); err != nil {
		b.Fatal(err)
	}
	world := FootWorld(sc)
	dt := 1.0 / 60.0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(sc, world, dt)
	}
}
