package npc

import (
	"math"
	"testing"

	"raytracer/internal/character"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
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
	path := FindPath(sc, start, goal, 0, character.DefaultNavigationParams())
	if len(path) < 2 {
		t.Fatalf("path = %v, want at least 2 corners", path)
	}
	nav := character.DefaultNavigationParams()
	for _, p := range path {
		if p.X > -0.5+nav.Radius && p.X < 0.5-nav.Radius && math.Abs(p.Z) < 1.0-nav.Radius {
			t.Fatalf("path corner %v passes through obstacle column", p)
		}
	}
	last := path[len(path)-1]
	if horizDist(last, goal) > navCellSize {
		t.Fatalf("path end %v too far from goal %v", last, goal)
	}
}

func TestNavigatorAdvancesPatrolWithinTargetRadius(t *testing.T) {
	n := NewNavigator(scene.NPCSpawn{
		Speed:  1,
		Patrol: []vec.V{{0, 0, 0}, {10, 0, 0}, {0, 0, 0}},
	})
	if n.targetRadius != navDefaultTargetRadius {
		t.Fatalf("targetRadius = %v, want %v", n.targetRadius, navDefaultTargetRadius)
	}
	nav := character.DefaultNavigationParams()
	hip := vec.V{X: 10, Z: 0.35} // within default radius of far waypoint
	n.wpIdx = 1
	if !n.atTarget(hip, 0, nav) {
		t.Fatal("expected atTarget within default radius")
	}
	n.wpIdx = 0
	if n.atTarget(vec.V{X: 2, Z: 0}, 0, nav) {
		t.Fatal("should not be at start waypoint from here")
	}
}

func TestNavigatorOvershootCountsAsArrival(t *testing.T) {
	n := NewNavigator(scene.NPCSpawn{Speed: 5, Patrol: []vec.V{{0, 0, 0}, {10, 0, 0}}})
	nav := character.NavigationParams{Radius: 0.95, ClearanceRadius: 1.45}
	n.wpIdx = 1
	// Past the waypoint heading +Z, still within overshoot band.
	hip := vec.V{X: 10.8, Z: 0.3}
	if !n.atTarget(hip, 180, nav) {
		t.Fatal("expected overshoot past waypoint to count as arrival")
	}
}

func TestNavigatorArrivalRadiusScalesWithSpeed(t *testing.T) {
	n := NewNavigator(scene.NPCSpawn{Speed: 5, Patrol: []vec.V{{0, 0, 0}}})
	nav := character.NavigationParams{ClearanceRadius: 1.45}
	r := n.arrivalRadius(nav)
	if r < 1.4 {
		t.Fatalf("arrival radius = %.2f, want >= 1.4 for fast spider-scale nav", r)
	}
}

func TestNavigatorPatrolLoopsOnNPCTestScene(t *testing.T) {
	sc, err := sceneio.Load(repoFile("scenes/npc-test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	a := &m.agents[0]
	startIdx := a.Nav.wpIdx
	seenReturn := false
	for i := 0; i < 3600; i++ {
		m.Update(sc, world, 1.0/60.0)
		if a.Nav.wpIdx == startIdx && i > 300 && horizDist(a.Spawn, a.LocomotorState().HipPos) < a.Nav.arrivalRadius(a.Rig.Navigation) {
			seenReturn = true
			break
		}
	}
	if !seenReturn {
		t.Fatalf("patrol never returned to start after %d frames: hip=%v wpIdx=%d",
			3600, a.LocomotorState().HipPos, a.Nav.wpIdx)
	}
}

func TestNavigatorPatrolAdvancesHeading(t *testing.T) {
	sc := navTestScene()
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
	if math.Abs(a.LocomotorState().Heading-wantHeading) > 5 {
		t.Fatalf("initial heading = %v, want ~%v (+X)", a.LocomotorState().Heading, wantHeading)
	}
	for i := 0; i < 240; i++ {
		m.Update(sc, FootWorld(sc), 1.0/60.0)
	}
	if a.LocomotorState().HipPos.X <= -0.5 {
		t.Fatalf("hip X = %v, expected forward progress toward patrol", a.LocomotorState().HipPos.X)
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

