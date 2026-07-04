package door

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	stateClosed  = "closed"
	stateOpening = "opening"
	stateOpen    = "open"
	stateClosing = "closing"
)

type panelXforms struct {
	boxes     map[int]*scene.Transform
	spheres   map[int]*scene.Transform
	cylinders map[int]*scene.Transform
}

// Panel tracks one swinging door leaf.
type Panel struct {
	Geom         scene.DoorPanelGeom
	Hinge        vec.V
	closed       panelXforms
	Angle        float64
	Target       float64
	OpenSign     float64
}

// Agent is one runtime door instance.
type Agent struct {
	ID             string
	Kind           string
	Axis           string
	ClosedAngle    float64
	OpenAngle      float64
	Swing          string
	OpenSign       float64
	Speed          float64
	Panels         []Panel
	State          string
	PanelCollision bool
	Interact       *scene.Interactable
}

func newAgent(spec scene.DoorSpec) *Agent {
	a := &Agent{
		ID:             spec.ID,
		Kind:           spec.Kind,
		Axis:           spec.Axis,
		ClosedAngle:    spec.ClosedAngle,
		OpenAngle:      spec.OpenAngle,
		Swing:          spec.Swing,
		OpenSign:       spec.OpenSign,
		Speed:          spec.Speed,
		State:          stateClosed,
		PanelCollision: true,
		Interact:       spec.Interact,
	}
	if a.Axis == "" {
		a.Axis = "y"
	}
	if a.Speed <= 0 {
		a.Speed = 1.5
	}
	if a.OpenAngle <= 0 {
		a.OpenAngle = math.Pi / 2
	}
	if a.OpenSign == 0 {
		a.OpenSign = 1
	}
	if a.Kind == "" {
		a.Kind = "single"
	}
	if a.Swing == "" {
		a.Swing = "one_way"
	}

	switch a.Kind {
	case "double":
		if len(spec.Panels) < 2 {
			return nil
		}
		a.Panels = []Panel{
			{Geom: spec.Panels[0], Hinge: spec.Hinge, OpenSign: -1},
			{Geom: spec.Panels[1], Hinge: spec.HingeRight, OpenSign: 1},
		}
	default:
		if len(spec.Panels) < 1 {
			return nil
		}
		a.Panels = []Panel{
			{Geom: spec.Panels[0], Hinge: spec.Hinge, OpenSign: a.OpenSign},
		}
	}
	return a
}

func (a *Agent) panelMaxAngle() float64 {
	return a.OpenAngle
}

func (a *Agent) isAnimating() bool {
	return a.State == stateOpening || a.State == stateClosing
}

func (a *Agent) toggle(playerPos vec.V) {
	switch a.State {
	case stateClosed, stateClosing:
		sign := a.OpenSign
		if a.Swing == "both" {
			sign = a.pickSwingSign(playerPos)
		}
		a.OpenSign = sign
		for i := range a.Panels {
			os := sign
			if a.Kind == "double" {
				os = sign * panelSideSign(i)
			}
			a.Panels[i].OpenSign = os
			a.Panels[i].Target = a.ClosedAngle + os*a.panelMaxAngle()
		}
		a.State = stateOpening
		a.PanelCollision = false
	case stateOpen, stateOpening:
		for i := range a.Panels {
			a.Panels[i].Target = a.ClosedAngle
		}
		a.State = stateClosing
		a.PanelCollision = false
	}
}

func panelSideSign(i int) float64 {
	if i == 0 {
		return -1
	}
	return 1
}

// pickSwingSign chooses which way a both-way door opens from the player's side
// of the hinge plane in the closed pose.
func (a *Agent) pickSwingSign(playerPos vec.V) float64 {
	if len(a.Panels) == 0 {
		return a.OpenSign
	}
	p := a.Panels[0]
	closedNormal := a.closedFaceNormal(p)
	toPlayer := playerPos.Sub(p.Hinge)
	if toPlayer.Dot(closedNormal) >= 0 {
		return 1
	}
	return -1
}

func (a *Agent) closedFaceNormal(p Panel) vec.V {
	idx := p.Geom.PrimaryBox()
	if idx < 0 {
		return vec.V{Z: 1}
	}
	if base := p.closed.boxes[idx]; base != nil {
		switch a.Axis {
		case "x":
			return base.RotateDir(vec.V{X: 1})
		case "z":
			return base.RotateDir(vec.V{Z: 1})
		default:
			return base.RotateDir(vec.V{Z: 1})
		}
	}
	return vec.V{Z: 1}
}

func (p Panel) boxIndices() []int {
	return rangeIndices(p.Geom.Boxes)
}

func rangeIndices(r [2]int) []int {
	if r[0] >= r[1] {
		return nil
	}
	out := make([]int, r[1]-r[0])
	for i := range out {
		out[i] = r[0] + i
	}
	return out
}

func (a *Agent) boxIndices() []int {
	var out []int
	for _, p := range a.Panels {
		out = append(out, p.boxIndices()...)
	}
	return out
}
