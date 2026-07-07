package npc

import (
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestOfficeSunsetNPCPatrolMoves(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "office-sunset", "index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	if len(m.agents) == 0 {
		t.Skip("server-room NPC spawn is commented out in server-room-1.toml")
	}
	if len(m.agents) != 1 || m.agents[0].Nav == nil {
		t.Fatalf("agents=%d nav=%v", len(m.agents), len(m.agents) == 1 && m.agents[0].Nav != nil)
	}
	start := m.agents[0].Locomotor.HipPos
	wp0 := m.agents[0].Nav.currentWaypoint()
	if p := FindPath(sc, start, wp0, m.agents[0].Locomotor.GroundY); len(p) == 0 {
		t.Fatalf("no path from start=%v to wp0=%v groundY=%v", start, wp0, m.agents[0].Locomotor.GroundY)
	}
	wp1 := m.agents[0].Nav.patrol[1]
	if p := FindPath(sc, start, wp1, m.agents[0].Locomotor.GroundY); len(p) == 0 {
		t.Fatalf("no path from start=%v to wp1=%v groundY=%v", start, wp1, m.agents[0].Locomotor.GroundY)
	}
	maxDisp := 0.0
	for i := 0; i < 180; i++ {
		m.Update(sc, world, 1.0/60.0)
		if d := horizDist(start, m.agents[0].Locomotor.HipPos); d > maxDisp {
			maxDisp = d
		}
	}
	end := m.agents[0].Locomotor.HipPos
	if maxDisp < 0.6 {
		t.Fatalf("office patrol barely moved: start=%v end=%v max_disp=%v", start, end, maxDisp)
	}
}
