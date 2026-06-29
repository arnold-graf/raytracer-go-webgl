package npc

import (
	"math"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func navTestScene() *scene.Scene {
	sc := &scene.Scene{}
	sc.Boxes = append(sc.Boxes,
		scene.Box{
			Min: vec.V{X: -10, Y: 0, Z: -10},
			Max: vec.V{X: 10, Y: 0.05, Z: 10},
		},
		scene.Box{
			Min: vec.V{X: -0.5, Y: 0, Z: -1.0},
			Max: vec.V{X: 0.5, Y: 2.0, Z: 1.0},
		},
	)
	return sc
}

func TestFindPathAroundObstacle(t *testing.T) {
	sc := navTestScene()
	start := vec.V{X: -3, Z: 0}
	goal := vec.V{X: 3, Z: 0}
	path := FindPath(sc, start, goal, 0)
	if len(path) < 2 {
		t.Fatalf("path = %v, want at least 2 corners", path)
	}
	for _, p := range path {
		if p.X > -0.5+navAgentRadius && p.X < 0.5-navAgentRadius && math.Abs(p.Z) < 1.0-navAgentRadius {
			t.Fatalf("path corner %v passes through obstacle column", p)
		}
	}
	last := path[len(path)-1]
	if horizDist(last, goal) > navCellSize {
		t.Fatalf("path end %v too far from goal %v", last, goal)
	}
}

func TestNavigatorPatrolAdvancesHeading(t *testing.T) {
	sc := &scene.Scene{}
	sc.NPCSpawns = []scene.NPCSpawn{{
		Rig:   "data/rigs/humanoid.yaml",
		Pose:  "idle",
		Pos:   vec.V{X: -1, Z: 0},
		Speed: 1.0,
		Patrol: []vec.V{
			{X: -1, Z: 0},
			{X: 4, Z: 0},
			{X: -1, Z: 0},
		},
	}}
	m := NewManager()
	if err := m.Instantiate(sc, FootWorld(sc)); err != nil {
		t.Fatal(err)
	}
	a := &m.agents[0]
	if a.Nav == nil {
		t.Fatal("expected navigator")
	}
	wantHeading := navHeadingFromDelta(5, 0) // toward +X
	if math.Abs(a.Locomotor.Heading-wantHeading) > 5 {
		t.Fatalf("initial heading = %v, want ~%v (+X)", a.Locomotor.Heading, wantHeading)
	}
	for i := 0; i < 120; i++ {
		m.Update(sc, FootWorld(sc), 1.0/60.0)
	}
	if a.Locomotor.HipPos.X <= -0.5 {
		t.Fatalf("hip X = %v, expected forward progress toward patrol", a.Locomotor.HipPos.X)
	}
}

func TestNPCPatrolTOMLLoads(t *testing.T) {
	sc := navTestScene()
	sc.NPCSpawns = []scene.NPCSpawn{{
		Rig:    "data/rigs/humanoid.yaml",
		Pos:    vec.V{},
		Speed:  1,
		Patrol: []vec.V{{X: 0, Z: 0}, {X: 2, Z: 0}},
	}}
	nav := NewNavigator(sc.NPCSpawns[0])
	if nav == nil || !nav.active {
		t.Fatal("expected active navigator from patrol spawn")
	}
}

