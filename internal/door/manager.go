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

func snapshotClosedBase(xf *scene.Transform, axis string, closedOffset float64, hinge vec.V) *scene.Transform {
	var base *scene.Transform
	if xf != nil {
		cpy := *xf
		base = &cpy
	}
	if closedOffset != 0 {
		deg := closedOffset * 180 / math.Pi
		off := scene.RotationAboutAxis(axis, deg, hinge)
		base = off.Compose(base)
	}
	return base
}

func snapshotPanelXforms(sc *scene.Scene, geom scene.DoorPanelGeom, axis string, closedOffset float64, hinge vec.V) panelXforms {
	out := panelXforms{
		boxes:     map[int]*scene.Transform{},
		spheres:   map[int]*scene.Transform{},
		cylinders: map[int]*scene.Transform{},
	}
	for i := geom.Boxes[0]; i < geom.Boxes[1]; i++ {
		out.boxes[i] = snapshotClosedBase(sc.Boxes[i].Xform, axis, closedOffset, hinge)
	}
	for i := geom.Spheres[0]; i < geom.Spheres[1]; i++ {
		out.spheres[i] = snapshotClosedBase(sc.Spheres[i].Xform, axis, closedOffset, hinge)
	}
	for i := geom.Cylinders[0]; i < geom.Cylinders[1]; i++ {
		out.cylinders[i] = snapshotClosedBase(sc.Cylinders[i].Xform, axis, closedOffset, hinge)
	}
	return out
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
			p := &a.Panels[j]
			if p.Geom.PrimaryBox() < 0 || p.Geom.PrimaryBox() >= len(sc.Boxes) {
				return fmt.Errorf("door[%d] %q: panel[%d] has no valid box", i, spec.ID, j)
			}
			var closedOffset float64
			if j < len(spec.PanelClosedAngles) {
				closedOffset = spec.PanelClosedAngles[j]
			}
			p.closed = snapshotPanelXforms(sc, p.Geom, a.Axis, closedOffset, p.Hinge)
		}
		sc.DynamicBodies = append(sc.DynamicBodies, doorDynamicBody(a))
		a.Interact = spec.Interact
		m.agents = append(m.agents, *a)
	}
	if len(m.agents) > 0 {
		m.applyAll(sc)
		sc.Touch()
	}
	return nil
}

func doorDynamicBody(a *Agent) scene.DynamicBody {
	body := scene.DynamicBody{Name: "door_" + a.ID}
	body.Boxes = unionRange(panelRanges(a, func(p Panel) [2]int { return p.Geom.Boxes }))
	body.Spheres = unionRange(panelRanges(a, func(p Panel) [2]int { return p.Geom.Spheres }))
	body.Cylinders = unionRange(panelRanges(a, func(p Panel) [2]int { return p.Geom.Cylinders }))
	return body
}

func panelRanges(a *Agent, pick func(Panel) [2]int) [][2]int {
	out := make([][2]int, len(a.Panels))
	for i, p := range a.Panels {
		out[i] = pick(p)
	}
	return out
}

func unionRange(ranges [][2]int) [2]int {
	min, max := -1, -1
	for _, r := range ranges {
		if r[0] >= r[1] {
			continue
		}
		if min < 0 || r[0] < min {
			min = r[0]
		}
		if r[1]-1 > max {
			max = r[1] - 1
		}
	}
	if min < 0 {
		return [2]int{0, 0}
	}
	return [2]int{min, max + 1}
}

// GhostBox returns true when the box should not block the player.
func (m *Manager) GhostBox(boxIndex int) bool {
	for i := range m.agents {
		a := &m.agents[i]
		if !a.PanelCollision {
			for _, idx := range a.boxIndices() {
				if idx == boxIndex {
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
		if PlayerOverlapsPanel(sc, p, feetY, headY, playerPos) {
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
