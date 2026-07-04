package sceneio

import (
	"fmt"
	"math"
	"path/filepath"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

type doorInteractDTO struct {
	Hint   string  `toml:"hint"`
	Range  float64 `toml:"use_range"`
	Center vec3    `toml:"center"`
}

type doorDTO struct {
	ID               string           `toml:"id"`
	Kind             string           `toml:"kind"`
	Hinge            vec3             `toml:"hinge"`
	HingeLeft        vec3             `toml:"hinge_left"`
	HingeRight       vec3             `toml:"hinge_right"`
	Axis             string           `toml:"axis"`
	OpenAngle        float64          `toml:"open_angle"`
	Swing            string           `toml:"swing"`
	OpenSign         float64          `toml:"open_sign"`
	Speed            float64          `toml:"speed"`
	PanelFile        string           `toml:"panel_file"`
	PanelLeftFile    string           `toml:"panel_left_file"`
	PanelRightFile   string           `toml:"panel_right_file"`
	PanelClosedAngle []float64        `toml:"panel_closed_angle"`
	Interact         *doorInteractDTO `toml:"interact"`
}

func (d doorDTO) baseSpec() scene.DoorSpec {
	kind := d.Kind
	if kind == "" {
		if d.PanelLeftFile != "" || d.PanelRightFile != "" {
			kind = "double"
		} else {
			kind = "single"
		}
	}
	hinge := d.Hinge.toV()
	hingeRight := d.HingeRight.toV()
	if kind == "double" {
		hinge = d.HingeLeft.toV()
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
	}
	if len(d.PanelClosedAngle) > 0 {
		spec.PanelClosedAngles = make([]float64, len(d.PanelClosedAngle))
		for i, deg := range d.PanelClosedAngle {
			spec.PanelClosedAngles[i] = deg * math.Pi / 180
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
	return spec
}

func (d doorDTO) resolve(s *scene.Scene, parentDir string, params map[string]any, seen map[string]bool, deps *[]string) (scene.DoorSpec, error) {
	if d.ID == "" {
		return scene.DoorSpec{}, fmt.Errorf("missing id")
	}
	spec := d.baseSpec()
	switch spec.Kind {
	case "double":
		if d.PanelLeftFile == "" || d.PanelRightFile == "" {
			return scene.DoorSpec{}, fmt.Errorf("double door requires panel_left_file and panel_right_file")
		}
		if spec.HingeRight == (vec.V{}) {
			return scene.DoorSpec{}, fmt.Errorf("double door requires hinge_right")
		}
		left, err := mergeDoorPanel(s, parentDir, d.PanelLeftFile, params, seen, deps)
		if err != nil {
			return scene.DoorSpec{}, fmt.Errorf("panel_left_file: %w", err)
		}
		right, err := mergeDoorPanel(s, parentDir, d.PanelRightFile, params, seen, deps)
		if err != nil {
			return scene.DoorSpec{}, fmt.Errorf("panel_right_file: %w", err)
		}
		spec.Panels = []scene.DoorPanelGeom{left, right}
	default:
		if d.PanelFile == "" {
			return scene.DoorSpec{}, fmt.Errorf("missing panel_file")
		}
		one, err := mergeDoorPanel(s, parentDir, d.PanelFile, params, seen, deps)
		if err != nil {
			return scene.DoorSpec{}, fmt.Errorf("panel_file: %w", err)
		}
		spec.Panels = []scene.DoorPanelGeom{one}
	}
	if err := validateDoorPanels(spec); err != nil {
		return scene.DoorSpec{}, err
	}
	return spec, nil
}

func validateDoorPanels(spec scene.DoorSpec) error {
	want := 1
	if spec.Kind == "double" {
		want = 2
	}
	if len(spec.Panels) < want {
		return fmt.Errorf("kind %q wants %d panel(s)", spec.Kind, want)
	}
	for i, p := range spec.Panels {
		if p.PrimaryBox() < 0 {
			return fmt.Errorf("panel[%d] has no box geometry", i)
		}
	}
	return nil
}

func mergeDoorPanel(dst *scene.Scene, parentDir, relPath string, params map[string]any, seen map[string]bool, deps *[]string) (scene.DoorPanelGeom, error) {
	panelPath := relPath
	if !filepath.IsAbs(panelPath) {
		panelPath = filepath.Join(parentDir, panelPath)
	}
	before := scene.CountPrimitives(dst)
	panel, err := load(panelPath, params, seen, deps, nil)
	if err != nil {
		return scene.DoorPanelGeom{}, err
	}
	if len(panel.DoorSpecs) > 0 {
		return scene.DoorPanelGeom{}, fmt.Errorf("%q: panel files must not contain [[door]]", relPath)
	}
	mergeScene(dst, panel, nil)
	after := scene.CountPrimitives(dst)
	return scene.DoorPanelGeom{
		Boxes:     [2]int{before.Boxes, after.Boxes},
		Spheres:   [2]int{before.Spheres, after.Spheres},
		Cylinders: [2]int{before.Cylinders, after.Cylinders},
	}, nil
}

func resolveDoors(s *scene.Scene, doors []doorDTO, parentDir string, params map[string]any, seen map[string]bool, deps *[]string) error {
	for i, d := range doors {
		spec, err := d.resolve(s, parentDir, params, seen, deps)
		if err != nil {
			return fmt.Errorf("door[%d]: %w", i, err)
		}
		s.DoorSpecs = append(s.DoorSpecs, spec)
	}
	return nil
}

func mergeDoorSpecs(dst *scene.Scene, sub *scene.Scene, xf *scene.Transform, boxOffset, sphereOffset, cylinderOffset int) {
	for _, ds := range sub.DoorSpecs {
		spec := ds
		for i := range spec.Panels {
			spec.Panels[i] = offsetPanelGeom(spec.Panels[i], boxOffset, sphereOffset, cylinderOffset)
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

func offsetPanelGeom(g scene.DoorPanelGeom, boxOff, sphOff, cylOff int) scene.DoorPanelGeom {
	if g.Boxes[0] < g.Boxes[1] {
		g.Boxes[0] += boxOff
		g.Boxes[1] += boxOff
	}
	if g.Spheres[0] < g.Spheres[1] {
		g.Spheres[0] += sphOff
		g.Spheres[1] += sphOff
	}
	if g.Cylinders[0] < g.Cylinders[1] {
		g.Cylinders[0] += cylOff
		g.Cylinders[1] += cylOff
	}
	return g
}
