package door

import (
	"fmt"
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/util"
	"raytracer/internal/vec"
)

// Manager owns runtime door agents and writes panel transforms into the scene.
type Manager struct {
	agents []Agent
}

// NewManager returns an empty door manager.
func NewManager() *Manager {
	return &Manager{}
}

// Instantiate spawns doors from scene DoorSpecs, registers panel DynamicBodies,
// and snapshots each panel's closed transform.
func (m *Manager) Instantiate(sc *scene.Scene) error {
	if sc == nil {
		return nil
	}
	m.agents = m.agents[:0]
	for i, spec := range sc.DoorSpecs {
		a := newAgent(spec)
		if a == nil {
			return fmt.Errorf("door[%d] %q: invalid spec", i, spec.ID)
		}
		for j := range a.Panels {
			idx := a.Panels[j].BoxIndex
			if idx < 0 || idx >= len(sc.Boxes) {
				return fmt.Errorf("door[%d] %q: panel_boxes[%d] out of range", i, spec.ID, j)
			}
			var base *scene.Transform
			if xf := sc.Boxes[idx].Xform; xf != nil {
				cpy := *xf
				base = &cpy
			}
			if j < len(spec.PanelClosedAngles) && spec.PanelClosedAngles[j] != 0 {
				deg := spec.PanelClosedAngles[j] * 180 / math.Pi
				off := scene.RotationAboutAxis(a.Axis, deg, a.Panels[j].Hinge)
				base = off.Compose(base)
			}
			a.Panels[j].ClosedBase = base
		}
		body := scene.DynamicBody{
			Name:  "door_" + spec.ID,
			Boxes: doorBoxRange(a),
		}
		sc.DynamicBodies = append(sc.DynamicBodies, body)
		a.Interact = spec.Interact
		m.agents = append(m.agents, *a)
	}
	if len(m.agents) > 0 {
		m.applyAll(sc)
		sc.Touch()
	}
	return nil
}

func doorBoxRange(a *Agent) [2]int {
	if len(a.Panels) == 0 {
		return [2]int{0, 0}
	}
	min, max := a.Panels[0].BoxIndex, a.Panels[0].BoxIndex+1
	for _, p := range a.Panels[1:] {
		if p.BoxIndex < min {
			min = p.BoxIndex
		}
		if p.BoxIndex+1 > max {
			max = p.BoxIndex + 1
		}
	}
	return [2]int{min, max}
}

// GhostBox returns true when the box should not block the player.
func (m *Manager) GhostBox(boxIndex int) bool {
	for i := range m.agents {
		a := &m.agents[i]
		if !a.PanelCollision {
			for _, p := range a.Panels {
				if p.BoxIndex == boxIndex {
					return true
				}
			}
		}
	}
	return false
}

// ToggleInteract opens or closes the door matching the activated interactable.
// Needed when the same door id appears multiple times (repeated [[include]]).
func (m *Manager) ToggleInteract(ia *scene.Interactable, playerPos vec.V) bool {
	if ia == nil {
		return false
	}
	var best *Agent
	bestD2 := math.Inf(1)
	for i := range m.agents {
		a := &m.agents[i]
		if a.Interact == nil {
			continue
		}
		if ia.DoorID != "" && a.ID != ia.DoorID {
			continue
		}
		dx := ia.Center.X - a.Interact.Center.X
		dy := ia.Center.Y - a.Interact.Center.Y
		dz := ia.Center.Z - a.Interact.Center.Z
		if dx*dx+dy*dy+dz*dz > 1e-8 {
			continue
		}
		dx = playerPos.X - a.Interact.Center.X
		dy = playerPos.Y - a.Interact.Center.Y
		dz = playerPos.Z - a.Interact.Center.Z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 < bestD2 {
			best = a
			bestD2 = d2
		}
	}
	if best == nil {
		if ia.DoorID != "" {
			return m.Toggle(nil, ia.DoorID, playerPos)
		}
		return m.ToggleNearest(nil, playerPos)
	}
	best.toggle(playerPos)
	return true
}

// Toggle opens or closes the door with the given id.
func (m *Manager) Toggle(sc *scene.Scene, id string, playerPos vec.V) bool {
	for i := range m.agents {
		if m.agents[i].ID == id {
			m.agents[i].toggle(playerPos)
			return true
		}
	}
	return false
}

// ToggleNearest toggles the in-range door closest to pos.
func (m *Manager) ToggleNearest(sc *scene.Scene, pos vec.V) bool {
	var best *Agent
	bestD2 := math.Inf(1)
	for i := range m.agents {
		a := &m.agents[i]
		if a.Interact == nil {
			continue
		}
		ia := a.Interact
		r := ia.Range
		if r <= 0 {
			r = 2.0
		}
		dx := pos.X - ia.Center.X
		dy := pos.Y - ia.Center.Y
		dz := pos.Z - ia.Center.Z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= r*r && d2 < bestD2 {
			best = a
			bestD2 = d2
		}
	}
	if best == nil {
		return false
	}
	best.toggle(pos)
	return true
}

// Update advances door animation and updates panel collision flags.
func (m *Manager) Update(sc *scene.Scene, playerPos vec.V, feetY, headY float64, dt float64) bool {
	if sc == nil || len(m.agents) == 0 {
		return false
	}
	changed := false
	skip := m.skipBoxFunc()
	for i := range m.agents {
		if m.stepAgent(sc, &m.agents[i], playerPos, feetY, headY, dt, skip) {
			changed = true
		}
	}
	if changed {
		sc.TouchTransforms()
	}
	return changed
}

func (m *Manager) skipBoxFunc() func(int) bool {
	doorBoxes := map[int]bool{}
	for i := range m.agents {
		for _, idx := range m.agents[i].boxIndices() {
			doorBoxes[idx] = true
		}
	}
	return func(i int) bool { return doorBoxes[i] }
}

func (m *Manager) stepAgent(sc *scene.Scene, a *Agent, playerPos vec.V, feetY, headY float64, dt float64, skip func(int) bool) bool {
	changed := false
	if a.isAnimating() {
		step := a.Speed * dt
		for pi := range a.Panels {
			p := &a.Panels[pi]
			proposed := util.StepToward(p.Angle, p.Target, step)
			if proposed != p.Angle && proposed != p.Target {
				proposed = clampAngle(sc, a, p, proposed, skip)
			}
			if math.Abs(proposed-p.Angle) > 1e-9 {
				p.Angle = proposed
			}
			applyPanelXform(sc, a, p, p.Angle)
			changed = true
		}
		allDone := true
		for _, p := range a.Panels {
			if !util.AtTarget(p.Angle, p.Target, 1e-4) {
				allDone = false
				break
			}
		}
		if allDone {
			if a.State == stateOpening {
				a.State = stateOpen
			} else if a.State == stateClosing {
				a.State = stateClosed
			}
		}
	}
	m.updateCollision(a, sc, playerPos, feetY, headY)
	return changed
}

func (m *Manager) updateCollision(a *Agent, sc *scene.Scene, playerPos vec.V, feetY, headY float64) {
	if a.isAnimating() {
		a.PanelCollision = false
		return
	}
	for _, p := range a.Panels {
		if PlayerOverlapsPanel(sc, p.BoxIndex, feetY, headY, playerPos) {
			a.PanelCollision = false
			return
		}
	}
	a.PanelCollision = true
}

func (m *Manager) applyAll(sc *scene.Scene) {
	for i := range m.agents {
		for j := range m.agents[i].Panels {
			applyPanelXform(sc, &m.agents[i], &m.agents[i].Panels[j], m.agents[i].Panels[j].Angle)
		}
	}
}
