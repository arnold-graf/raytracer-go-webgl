package npc

import (
	"math"
	"testing"

	"raytracer/internal/character"
	"raytracer/internal/vec"
)

func TestResolveSeparationPushesOverlappingAgents(t *testing.T) {
	m := NewManager()
	rig, err := character.LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m.rigs["spider"] = rig
	spawn := vec.V{X: 0, Y: 0, Z: 0}
	a := Agent{
		Name:   "a",
		Rig:    rig,
		Driver: character.NewPhysicsDriver(rig, spawn, 0, 1, flatSepWorld{}, "idle"),
	}
	b := Agent{
		Name:   "b",
		Rig:    rig,
		Driver: character.NewPhysicsDriver(rig, spawn, 0, 1, flatSepWorld{}, "idle"),
	}
	m.agents = []Agent{a, b}
	world := flatSepWorld{}
	m.resolveSeparation(world)
	d := horizDist(m.agents[0].Driver.HipPos(), m.agents[1].Driver.HipPos())
	want := pairSeparation(&m.agents[0], &m.agents[1])
	if d < want-0.08 {
		t.Fatalf("distance after resolve = %.2f, want >= %.2f", d, want)
	}
}

func TestResolveSeparationTripleOverlap(t *testing.T) {
	m := NewManager()
	rig, err := character.LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	spawn := vec.V{X: 0, Y: 0, Z: 0}
	for i := 0; i < 3; i++ {
		m.agents = append(m.agents, Agent{
			Name:   string(rune('a' + i)),
			Rig:    rig,
			Driver: character.NewPhysicsDriver(rig, spawn, 0, 1, flatSepWorld{}, "idle"),
		})
	}
	m.resolveSeparation(flatSepWorld{})
	want := pairSeparation(&m.agents[0], &m.agents[1])
	for i := range m.agents {
		for j := i + 1; j < len(m.agents); j++ {
			d := horizDist(m.agents[i].Driver.HipPos(), m.agents[j].Driver.HipPos())
			if d < want-0.08 {
				t.Fatalf("agents %d-%d distance %.2f, want >= %.2f", i, j, d, want)
			}
		}
	}
}

func TestSeparationHeadingSteersAway(t *testing.T) {
	rig, err := character.LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	self := &Agent{
		Rig:    rig,
		Driver: character.NewPhysicsDriver(rig, vec.V{X: 0, Z: 0}, 0, 1, flatSepWorld{}, "idle"),
	}
	peer := &Agent{
		Rig:    rig,
		Driver: character.NewPhysicsDriver(rig, vec.V{X: 2, Z: 0}, 0, 1, flatSepWorld{}, "idle"),
	}
	peers := []*Agent{self, peer}
	// Base heading toward +X (toward peer); separation should pull heading away.
	base := navHeadingFromDelta(1, 0)
	adj := separationHeading(vec.V{}, base, self, peers)
	if math.Abs(normalizeAngleDelta(adj-base)) < 15 {
		t.Fatalf("expected separation to deflect heading, base=%.1f adj=%.1f", base, adj)
	}
}

func normalizeAngleDelta(a float64) float64 {
	return math.Mod(a+540, 360) - 180
}

type flatSepWorld struct{}

func (flatSepWorld) GroundHeight(_, _, _ float64) float64 { return 0 }
func (flatSepWorld) GroundNormal(_, _, _ float64) vec.V   { return vec.V{Y: 1} }
