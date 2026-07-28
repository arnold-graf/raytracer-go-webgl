package npc

import (
	"path/filepath"
	"testing"
	"time"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestVillaPatrolPathfindCost(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	a := &m.agents[0]
	nav := a.Rig.Navigation
	from := vec.V{X: 0, Z: 5}
	to := vec.V{X: 0, Z: 40}

	gridStart := time.Now()
	g := buildNavGrid(sc, from, to, a.LocomotorState().GroundY, nav)
	gridElapsed := time.Since(gridStart)
	t.Logf("buildNavGrid: %dx%d=%d cells (cell=%.2fm) in %v", g.cols, g.rows, g.cols*g.rows, g.cellSize, gridElapsed)
	if gridElapsed > 200*time.Millisecond {
		t.Fatalf("buildNavGrid took %v, want <200ms", gridElapsed)
	}

	start := time.Now()
	path := FindPath(sc, from, to, a.LocomotorState().GroundY, nav)
	elapsed := time.Since(start)
	t.Logf("FindPath z=5->40: %d waypoints, total %v", len(path), elapsed)

	start = time.Now()
	path2 := FindPath(sc, to, from, a.LocomotorState().GroundY, nav)
	t.Logf("FindPath z=40->5: %d waypoints, total %v", len(path2), time.Since(start))

	// Patrol cache should make the second leg instant on repeat.
	nav2 := NewNavigator(sc.NPCSpawns[0])
	nav2.pathCache = make(map[int][]vec.V)
	_ = findPatrolPath(sc, nav2.patrol, 0, 1, a.LocomotorState().GroundY, nav, nav2.pathCache)
	cacheStart := time.Now()
	_ = findPatrolPath(sc, nav2.patrol, 0, 1, a.LocomotorState().GroundY, nav, nav2.pathCache)
	if d := time.Since(cacheStart); d > time.Millisecond {
		t.Fatalf("cached patrol path took %v, want <1ms", d)
	}
}
