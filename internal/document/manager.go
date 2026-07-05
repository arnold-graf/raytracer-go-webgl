package document

import (
	"fmt"

	"raytracer/internal/anim"
	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	animSec    = 0.55
	readDist   = 0.25
	useRange   = 1.2
	paperThick = 0.002
)

// Manager owns runtime document agents and writes paper transforms into the scene.
type Manager struct {
	agents []agent
}

type agent struct {
	spec     scene.DocumentSpec
	boxIndex int
	rest     *scene.Transform
	present  anim.Channel
	interact *scene.Interactable
}

// NewManager returns an empty document manager.
func NewManager() *Manager { return &Manager{} }

// Instantiate spawns paper boxes from scene DocumentSpecs and registers interactables.
func (m *Manager) Instantiate(sc *scene.Scene) error {
	if sc == nil {
		return nil
	}
	m.agents = m.agents[:0]
	for i, spec := range sc.DocumentSpecs {
		a, err := spawnAgent(sc, spec)
		if err != nil {
			return fmt.Errorf("document[%d]: %w", i, err)
		}
		m.agents = append(m.agents, a)
	}
	if len(m.agents) > 0 {
		sc.Touch()
	}
	return nil
}

func spawnAgent(sc *scene.Scene, spec scene.DocumentSpec) (agent, error) {
	w := spec.Width
	h := spec.Height
	d := spec.Depth
	if w <= 0 {
		w = 0.21
	}
	if h <= 0 {
		h = 0.297
	}
	if d <= 0 {
		d = paperThick
	}
	rest := spec.Rest
	if rest == nil {
		center := vec.New(spec.PosX+w/2, spec.PosY+h/2, spec.PosZ+d/2)
		rest = scene.NewRigidTransform(spec.RotateX, spec.RotateY, spec.RotateZ, center)
	}

	boxStart := len(sc.Boxes)
	sc.Boxes = append(sc.Boxes, scene.Box{
		Min: vec.New(-w/2, -h/2, -d/2),
		Max: vec.New(w/2, h/2, d/2),
		Surface: scene.Surface{
			Mat:    scene.MatDiffuse,
			Albedo: spec.Albedo,
			Tex:    spec.TexID,
			IOR:    1.5,
			Xform:  rest.Clone(),
		},
	})
	sc.DynamicBodies = append(sc.DynamicBodies, scene.DynamicBody{
		Name:  "document_" + spec.ID,
		Boxes: [2]int{boxStart, len(sc.Boxes)},
	})

	ia := spec.Interact
	if ia != nil {
		dup := *ia
		ia = &dup
	}
	return agent{
		spec:     spec,
		boxIndex: boxStart,
		rest:     rest,
		present:  anim.Channel{Duration: animSec},
		interact: ia,
	}, nil
}

// Reading reports whether the player is holding a document open (movement locked).
func (m *Manager) Reading() bool {
	for i := range m.agents {
		if m.agents[i].present.Active {
			return true
		}
	}
	return false
}

// GhostBox returns true when the paper should not block the player.
func (m *Manager) GhostBox(boxIndex int) bool {
	for i := range m.agents {
		a := &m.agents[i]
		if a.boxIndex == boxIndex && a.present.Engaged() {
			return true
		}
	}
	return false
}

// ToggleInteract opens or closes the document matching the activated interactable.
// The second return is the optional on_use handler id when the document was opened.
func (m *Manager) ToggleInteract(sc *scene.Scene, ia *scene.Interactable) (opened bool, onUse string) {
	if ia == nil {
		return false, ""
	}
	for i := range m.agents {
		a := &m.agents[i]
		if a.interact == nil {
			continue
		}
		if ia.DocumentID != "" && a.spec.ID != ia.DocumentID {
			continue
		}
		if ia.DocumentID == "" && ia.Center != a.interact.Center {
			continue
		}
		wasOpen := a.present.Engaged()
		a.toggle(sc)
		if !wasOpen {
			return true, a.spec.OnUse
		}
		return false, ""
	}
	return false, ""
}

// Dismiss closes whichever document is currently open (E while reading).
func (m *Manager) Dismiss(sc *scene.Scene) bool {
	for i := range m.agents {
		if m.agents[i].present.Active {
			m.agents[i].close(sc)
			return true
		}
	}
	return false
}

func (a *agent) toggle(sc *scene.Scene) {
	if a.present.Engaged() {
		a.close(sc)
	} else {
		a.present.Open()
	}
}

func (a *agent) close(sc *scene.Scene) {
	from := snapshotPose(sc, a.boxIndex, a.rest)
	if from != nil && sc != nil {
		sc.Boxes[a.boxIndex].Xform = from.Clone()
	}
	a.present.Close(from)
}

// Update animates paper pose and follows the camera while reading.
func (m *Manager) Update(sc *scene.Scene, cam *camera.Camera, dt float64) bool {
	if sc == nil || cam == nil || len(m.agents) == 0 {
		return false
	}
	changed := false
	for i := range m.agents {
		a := &m.agents[i]
		if !a.present.Engaged() {
			continue
		}
		target := anim.PresentToCamera(cam, readDist)
		pose, ch := a.present.Update(dt, a.rest, target)
		sc.Boxes[a.boxIndex].Xform = pose
		if ch {
			changed = true
		}
	}
	if changed {
		sc.TouchTransforms()
	}
	return changed
}

func snapshotPose(sc *scene.Scene, boxIndex int, fallback *scene.Transform) *scene.Transform {
	if sc != nil && boxIndex >= 0 && boxIndex < len(sc.Boxes) {
		if xf := sc.Boxes[boxIndex].Xform; xf != nil {
			return xf.Clone()
		}
	}
	return fallback.Clone()
}
