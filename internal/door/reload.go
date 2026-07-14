package door

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// PanelSnap is one door leaf's animation state preserved across hot reload.
type PanelSnap struct {
	Angle    float64
	Target   float64
	OpenSign float64
}

// AgentSnap is one door agent's runtime state preserved across hot reload.
type AgentSnap struct {
	ID             string
	Key            vec.V // scene-space disambiguator (hinge or closed panel center)
	State          string
	OpenSign       float64
	OpenElapsed    float64
	PanelCollision bool
	Panels         []PanelSnap
}

const hingeMatchTol = 0.05 // metres; doors moved farther are treated as new

// Snapshot records door runtime state before a scene hot reload.
func (m *Manager) Snapshot(sc *scene.Scene) []AgentSnap {
	if m == nil || len(m.agents) == 0 {
		return nil
	}
	out := make([]AgentSnap, len(m.agents))
	for i := range m.agents {
		a := &m.agents[i]
		snap := AgentSnap{
			ID:             a.ID,
			Key:            agentSnapKey(sc, a),
			State:          a.State,
			OpenSign:       a.OpenSign,
			OpenElapsed:    a.OpenElapsed,
			PanelCollision: a.PanelCollision,
			Panels:         make([]PanelSnap, len(a.Panels)),
		}
		for j, p := range a.Panels {
			snap.Panels[j] = PanelSnap{
				Angle: p.Angle, Target: p.Target, OpenSign: p.OpenSign,
			}
		}
		out[i] = snap
	}
	return out
}

// Restore reapplies a snapshot after Instantiate on a reloaded scene.
func (m *Manager) Restore(sc *scene.Scene, saved []AgentSnap) {
	if m == nil || sc == nil || len(saved) == 0 || len(m.agents) == 0 {
		return
	}
	used := make([]bool, len(saved))
	tol2 := hingeMatchTol * hingeMatchTol
	for i := range m.agents {
		a := &m.agents[i]
		key := agentSnapKey(sc, a)
		idx := -1
		bestD2 := math.Inf(1)
		for j, s := range saved {
			if used[j] || s.ID != a.ID {
				continue
			}
			dx := key.X - s.Key.X
			dy := key.Y - s.Key.Y
			dz := key.Z - s.Key.Z
			d2 := dx*dx + dy*dy + dz*dz
			if d2 < bestD2 {
				bestD2 = d2
				idx = j
			}
		}
		if idx < 0 || bestD2 > tol2 {
			continue
		}
		used[idx] = true
		s := saved[idx]
		a.State = s.State
		a.OpenSign = s.OpenSign
		a.OpenElapsed = s.OpenElapsed
		a.PanelCollision = s.PanelCollision
		for j := range a.Panels {
			if j >= len(s.Panels) {
				break
			}
			a.Panels[j].Angle = s.Panels[j].Angle
			a.Panels[j].Target = s.Panels[j].Target
			a.Panels[j].OpenSign = s.Panels[j].OpenSign
		}
	}
	m.applyAll(sc)
	sc.Touch()
}

func agentSnapKey(sc *scene.Scene, a *Agent) vec.V {
	if len(a.Panels) == 0 {
		return vec.V{}
	}
	if a.isSliding() {
		return panelClosedWorldCenter(sc, a.Panels[0])
	}
	return a.Panels[0].Hinge
}

func panelClosedWorldCenter(sc *scene.Scene, p Panel) vec.V {
	idx := p.Geom.PrimaryBox()
	if sc == nil || idx < 0 || idx >= len(sc.Boxes) {
		return vec.V{}
	}
	b := &sc.Boxes[idx]
	local := vec.New(
		(b.Min.X+b.Max.X)/2,
		(b.Min.Y+b.Max.Y)/2,
		(b.Min.Z+b.Max.Z)/2,
	)
	if base := p.closed.boxes[idx]; base != nil {
		return base.ToWorld(local)
	}
	if b.Xform != nil {
		return b.Xform.ToWorld(local)
	}
	return local
}
