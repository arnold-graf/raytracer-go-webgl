package interactlight

import (
	"fmt"
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const fadeSec = 0.2

// Manager toggles interactive scene lights with a short brightness fade.
type Manager struct {
	agents []agent
}

type agent struct {
	lightIdx  int
	baseColor vec.V
	on        bool
	fade      float64
	body      scene.DynamicBody
	interact  *scene.Interactable
}

// NewManager returns an empty interactive-light manager.
func NewManager() *Manager {
	return &Manager{}
}

// Instantiate registers interactive lights from sc for ray picking and GPU updates.
// skip, when non-nil, returns true for light indices managed elsewhere (e.g. reactive state).
func (m *Manager) Instantiate(sc *scene.Scene, skip func(int) bool) {
	if m == nil || sc == nil {
		return
	}
	m.agents = m.agents[:0]
	for i, l := range sc.Lights {
		if !l.Interactive {
			continue
		}
		if skip != nil && skip(i) {
			continue
		}
		hint := l.Hint
		if hint == "" {
			hint = "lamp"
		}
		iaIdx := sc.RegisterInteractable(scene.Interactable{
			Hint:       hint,
			Handler:    "light_toggle",
			LightIndex: i,
		})
		sc.SetLightInteract(i, iaIdx)
		body := scene.DynamicBody{
			Name:   fmt.Sprintf("interactive_light_%d", i),
			Lights: [2]int{i, i + 1},
		}
		sc.DynamicBodies = append(sc.DynamicBodies, body)
		m.agents = append(m.agents, agent{
			lightIdx:  i,
			baseColor: l.Color,
			on:        true,
			fade:      1,
			body:      body,
			interact:  &sc.Interactables[iaIdx],
		})
	}
	if len(m.agents) > 0 {
		sc.TouchTransforms()
	}
}

// Rebind rebuilds agents after structural scene edits shifted light indices.
func (m *Manager) Rebind(sc *scene.Scene, skip func(int) bool) {
	if m == nil || sc == nil {
		return
	}
	prev := make(map[int]agent, len(m.agents))
	for _, a := range m.agents {
		if a.interact != nil {
			prev[a.interact.Index()] = a
		}
	}
	m.agents = m.agents[:0]
	for i, l := range sc.Lights {
		if !l.Interactive {
			continue
		}
		if skip != nil && skip(i) {
			continue
		}
		iaIdx, ok := sc.LightInteractIndex(i)
		if !ok || iaIdx < 0 || iaIdx >= len(sc.Interactables) {
			continue
		}
		sc.Interactables[iaIdx].LightIndex = i
		a := agent{
			lightIdx:  i,
			baseColor: l.Color,
			on:        true,
			fade:      1,
			interact:  &sc.Interactables[iaIdx],
		}
		if old, ok := prev[iaIdx]; ok {
			a.on = old.on
			a.fade = old.fade
			a.baseColor = old.baseColor
		}
		m.agents = append(m.agents, a)
	}
}

// ToggleInteract flips the targeted interactive light on or off.
func (m *Manager) ToggleInteract(ia *scene.Interactable) {
	if m == nil || ia == nil || ia.LightIndex < 0 {
		return
	}
	for i := range m.agents {
		if m.agents[i].lightIdx == ia.LightIndex {
			m.agents[i].on = !m.agents[i].on
			return
		}
	}
}

// Update animates brightness fades and returns whether the scene lights changed.
func (m *Manager) Update(sc *scene.Scene, dt float64) bool {
	if m == nil || sc == nil || len(m.agents) == 0 {
		return false
	}
	changed := false
	speed := 1.0 / fadeSec
	for i := range m.agents {
		a := &m.agents[i]
		if a.lightIdx < 0 || a.lightIdx >= len(sc.Lights) {
			continue
		}
		target := 0.0
		if a.on {
			target = 1
		}
		prev := a.fade
		if a.fade < target {
			a.fade = math.Min(target, a.fade+speed*dt)
		} else if a.fade > target {
			a.fade = math.Max(target, a.fade-speed*dt)
		}
		if a.fade == prev {
			continue
		}
		changed = true
		e := scene.SmoothStep(a.fade)
		sc.Lights[a.lightIdx].Color = a.baseColor.Scale(e)
	}
	if changed {
		sc.TouchTransforms()
	}
	return changed
}
