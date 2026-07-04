package sceneio

import (
	"fmt"
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

type doorInteractDTO struct {
	Hint   string  `toml:"hint"`
	Range  float64 `toml:"use_range"`
	Center vec3    `toml:"center"`
}

type doorDTO struct {
	ID         string           `toml:"id"`
	Kind       string           `toml:"kind"`
	Hinge      vec3             `toml:"hinge"`
	HingeLeft  vec3             `toml:"hinge_left"`
	HingeRight vec3             `toml:"hinge_right"`
	Axis       string           `toml:"axis"`
	OpenAngle  float64          `toml:"open_angle"`
	Swing      string           `toml:"swing"`
	OpenSign   float64          `toml:"open_sign"`
	Speed              float64          `toml:"speed"`
	PanelBoxes         []int            `toml:"panel_boxes"`
	PanelClosedAngle   []float64        `toml:"panel_closed_angle"`
	Interact           *doorInteractDTO `toml:"interact"`
}

func (d doorDTO) build(boxCount int) (scene.DoorSpec, error) {
	if d.ID == "" {
		return scene.DoorSpec{}, fmt.Errorf("missing id")
	}
	kind := d.Kind
	if kind == "" {
		kind = "single"
	}
	hinge := d.Hinge.toV()
	hingeRight := d.HingeRight.toV()
	if kind == "double" {
		hinge = d.HingeLeft.toV()
		if hingeRight == (vec.V{}) {
			return scene.DoorSpec{}, fmt.Errorf("double door requires hinge_right")
		}
	}
	openAngle := d.OpenAngle
	if openAngle <= 0 {
		openAngle = 90
	}
	spec := scene.DoorSpec{
		ID:          d.ID,
		Kind:        kind,
		Hinge:       hinge,
		HingeRight:  hingeRight,
		Axis:        d.Axis,
		ClosedAngle: 0,
		OpenAngle:   openAngle * math.Pi / 180,
		Swing:       d.Swing,
		OpenSign:    d.OpenSign,
		Speed:       d.Speed,
		PanelBoxes:  append([]int(nil), d.PanelBoxes...),
	}
	if len(d.PanelClosedAngle) > 0 {
		spec.PanelClosedAngles = make([]float64, len(d.PanelClosedAngle))
		for i, deg := range d.PanelClosedAngle {
			spec.PanelClosedAngles[i] = deg * math.Pi / 180
		}
	}
	if len(spec.PanelBoxes) == 0 {
		return scene.DoorSpec{}, fmt.Errorf("panel_boxes required")
	}
	want := 1
	if kind == "double" {
		want = 2
	}
	if len(spec.PanelBoxes) < want {
		return scene.DoorSpec{}, fmt.Errorf("panel_boxes wants %d indices for kind %q", want, kind)
	}
	for _, idx := range spec.PanelBoxes {
		if idx < 0 || idx >= boxCount {
			return scene.DoorSpec{}, fmt.Errorf("panel_boxes index %d out of range (have %d boxes)", idx, boxCount)
		}
	}
	if d.Interact != nil {
		ia := scene.Interactable{
			Hint:    d.Interact.Hint,
			Handler: "door",
			Range:   d.Interact.Range,
			Center:  d.Interact.Center.toV(),
			DoorID:  d.ID,
		}
		if ia.Hint == "" {
			ia.Hint = "press {{use_button}} to open"
		}
		spec.Interact = &ia
	}
	return spec, nil
}

func mergeDoorSpecs(dst *scene.Scene, sub *scene.Scene, xf *scene.Transform, boxOffset int) {
	for _, ds := range sub.DoorSpecs {
		spec := ds
		for i := range spec.PanelBoxes {
			spec.PanelBoxes[i] += boxOffset
		}
		if xf != nil {
			spec.Hinge = xf.ToWorld(spec.Hinge)
			spec.HingeRight = xf.ToWorld(spec.HingeRight)
			if spec.Interact != nil {
				ia := *spec.Interact
				ia.Center = xf.ToWorld(ia.Center)
				spec.Interact = &ia
			}
		}
		dst.DoorSpecs = append(dst.DoorSpecs, spec)
	}
}
