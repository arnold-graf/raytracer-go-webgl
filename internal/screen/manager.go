package screen

import (
	"fmt"

	"raytracer/internal/anim"
	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	animSec      = 0.55
	panelThick   = 0.002
)

// Manager owns runtime screen agents and drives the view-camera animation.
type Manager struct {
	agents []agent
}

type agent struct {
	spec       scene.ScreenSpec
	boxIndex   int
	rest       *scene.Transform
	interact   *scene.Interactable
	savedPose  camera.Pose
	viewPose   camera.Pose
	viewPos    vec.V
	channel    anim.Channel
	closeStart camera.Pose
}

// NewManager returns an empty screen manager.
func NewManager() *Manager { return &Manager{} }

// Instantiate spawns screen boxes from scene ScreenSpecs and registers interactables.
func (m *Manager) Instantiate(sc *scene.Scene) error {
	if sc == nil {
		return nil
	}
	m.agents = m.agents[:0]
	for i, spec := range sc.ScreenSpecs {
		a, err := spawnAgent(sc, spec)
		if err != nil {
			return fmt.Errorf("screen[%d]: %w", i, err)
		}
		m.agents = append(m.agents, a)
	}
	if len(m.agents) > 0 {
		sc.Touch()
	}
	return nil
}

func spawnAgent(sc *scene.Scene, spec scene.ScreenSpec) (agent, error) {
	w := spec.Width
	h := spec.Height
	d := spec.Depth
	if w <= 0 {
		w = 0.28
	}
	if h <= 0 {
		h = 0.23
	}
	if d <= 0 {
		d = panelThick
	}
	rest := spec.Rest
	if rest == nil {
		center := vec.New(spec.PosX+w/2, spec.PosY+h/2, spec.PosZ+d/2)
		rest = scene.NewRigidTransform(spec.RotateX, spec.RotateY, spec.RotateZ, center)
	}

	mat := spec.Mat
	boxStart := len(sc.Boxes)
	sc.Boxes = append(sc.Boxes, scene.Box{
		Min: vec.New(-w/2, -h/2, -d/2),
		Max: vec.New(w/2, h/2, d/2),
		Surface: scene.Surface{
			Mat:     mat,
			Albedo:  spec.Albedo,
			Rough:   spec.Rough,
			Reflect: spec.Reflect,
			Tex:     spec.TexID,
			IOR:     1.5,
			Xform:   rest.Clone(),
		},
	})
	sc.DynamicBodies = append(sc.DynamicBodies, scene.DynamicBody{
		Name:  "screen_" + spec.ID,
		Boxes: [2]int{boxStart, len(sc.Boxes)},
	})

	var interact *scene.Interactable
	if spec.Interact != nil {
		iaIdx := sc.RegisterInteractable(*spec.Interact)
		sc.SetBoxInteract(boxStart, iaIdx)
		interact = &sc.Interactables[iaIdx]
	}
	return agent{
		spec:     spec,
		boxIndex: boxStart,
		rest:     rest,
		interact: interact,
		channel:  anim.Channel{Duration: animSec},
	}, nil
}

// Viewing reports whether the player is using a screen (movement locked).
func (m *Manager) Viewing() bool {
	for i := range m.agents {
		if m.agents[i].channel.Engaged() {
			return true
		}
	}
	return false
}

// ToggleInteract opens or closes the screen matching the activated interactable.
func (m *Manager) ToggleInteract(sc *scene.Scene, cam *camera.Camera, aspect float64, ia *scene.Interactable) (opened bool, onUse string) {
	if ia == nil || cam == nil || sc == nil {
		return false, ""
	}
	for i := range m.agents {
		a := &m.agents[i]
		if a.interact == nil {
			continue
		}
		if ia.ScreenID != "" && a.spec.ID != ia.ScreenID {
			continue
		}
		if ia.BoxIndex >= 0 && a.boxIndex != ia.BoxIndex {
			continue
		}
		wasOpen := a.channel.Engaged()
		a.toggle(sc, cam, aspect)
		if !wasOpen {
			return true, a.spec.OnUse
		}
		return false, ""
	}
	return false, ""
}

// Dismiss closes whichever screen is currently open (E while viewing).
func (m *Manager) Dismiss(sc *scene.Scene, cam *camera.Camera) bool {
	for i := range m.agents {
		if m.agents[i].channel.Engaged() {
			m.agents[i].close(cam)
			return true
		}
	}
	return false
}

func (a *agent) toggle(sc *scene.Scene, cam *camera.Camera, aspect float64) {
	if a.channel.Engaged() {
		a.close(cam)
		return
	}
	a.open(sc, cam, aspect)
}

func (a *agent) open(sc *scene.Scene, cam *camera.Camera, aspect float64) {
	a.savedPose = cam.Pose()
	a.viewPose = ViewPose(&sc.Boxes[a.boxIndex], aspect)
	a.viewPos = a.viewPose.Pos
	a.closeStart = camera.Pose{}
	a.channel.Open()
}

func (a *agent) close(cam *camera.Camera) {
	p := cam.Pose()
	p.Pos = a.viewPos
	a.closeStart = p
	a.channel.Close(nil)
}

// Update animates the camera toward or away from the screen view pose.
func (m *Manager) Update(sc *scene.Scene, cam *camera.Camera, aspect float64, dt float64) bool {
	if sc == nil || cam == nil || len(m.agents) == 0 {
		return false
	}
	changed := false
	for i := range m.agents {
		if ch := m.agents[i].update(sc, cam, dt); ch {
			changed = true
		}
	}
	return changed
}

func (a *agent) update(sc *scene.Scene, cam *camera.Camera, dt float64) bool {
	if !a.channel.Engaged() {
		return false
	}
	if a.channel.Closing {
		prev := a.channel.CloseT
		_, _ = a.channel.Update(dt, nil, nil)
		u := scene.SmoothStep(a.channel.CloseT)
		cam.SetPose(anim.LerpPose(a.closeStart, a.savedPose, u))
		return a.channel.CloseT != prev
	}

	if a.channel.OpenT < 1 {
		prev := a.channel.OpenT
		_, _ = a.channel.Update(dt, nil, nil)
		u := scene.SmoothStep(a.channel.OpenT)
		pose := anim.LerpPose(a.savedPose, a.viewPose, u)
		cam.SetPose(pose)
		if a.channel.OpenT >= 1 {
			a.viewPos = pose.Pos
		}
		return a.channel.OpenT != prev
	}

	p := cam.Pose()
	if p.Pos != a.viewPos {
		p.Pos = a.viewPos
		cam.SetPose(p)
		return true
	}
	return false
}
