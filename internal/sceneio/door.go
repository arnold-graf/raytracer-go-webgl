package sceneio

import (
	"fmt"
	"math"
	"path/filepath"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

type doorDTO struct {
	ID               string    `toml:"id"`
	Kind             string    `toml:"kind"`
	Hinge            vec3      `toml:"hinge"`
	HingeLeft        vec3      `toml:"hinge_left"`
	HingeRight       vec3      `toml:"hinge_right"`
	Axis             string    `toml:"axis"`
	Direction        string    `toml:"direction"`
	OpenAngle        float64   `toml:"open_angle"`
	OpenDistance     float64   `toml:"open_distance"`
	Swing            string    `toml:"swing"`
	OpenSign         float64   `toml:"open_sign"`
	Speed            float64   `toml:"speed"`
	PanelFile        string    `toml:"panel_file"`
	PanelLeftFile    string    `toml:"panel_left_file"`
	PanelRightFile   string    `toml:"panel_right_file"`
	PanelClosedAngle []float64 `toml:"panel_closed_angle"`
	CanClose         *bool     `toml:"can_close"`
	AutocloseTimeout *float64  `toml:"autoclose_timeout"`
	interactPropsDTO
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
	openDistance := d.OpenDistance
	if openDistance <= 0 {
		openDistance = 2.0
	}
	spec := scene.DoorSpec{
		ID:           d.ID,
		Kind:         kind,
		Hinge:        hinge,
		HingeRight:   hingeRight,
		Axis:         d.Axis,
		ClosedAngle:  0,
		OpenAngle:    openAngle * math.Pi / 180,
		OpenDistance: openDistance,
		SlideDir:     slideDirFromString(d.Direction),
		Swing:        d.Swing,
		OpenSign:     d.OpenSign,
		Speed:        d.Speed,
		CanClose:     true,
	}
	if d.CanClose != nil {
		spec.CanClose = *d.CanClose
	}
	if d.AutocloseTimeout != nil && *d.AutocloseTimeout > 0 {
		spec.AutocloseTimeout = *d.AutocloseTimeout
	}
	if len(d.PanelClosedAngle) > 0 {
		spec.PanelClosedAngles = make([]float64, len(d.PanelClosedAngle))
		for i, deg := range d.PanelClosedAngle {
			spec.PanelClosedAngles[i] = deg * math.Pi / 180
		}
	}
	hint := d.Hint
	if hint == "" {
		hint = "DOOR"
	}
	ia := scene.Interactable{
		Hint:    hint,
		Handler: "door",
		Range:   d.Range,
		DoorID:  d.ID,
	}
	spec.Interact = &ia
	return spec
}

func (d doorDTO) resolve(s *scene.Scene, parentDir string, params map[string]any, seen map[string]bool, deps *[]string) (scene.DoorSpec, error) {
	if d.ID == "" {
		return scene.DoorSpec{}, fmt.Errorf("missing id")
	}
	spec := d.baseSpec()
	switch spec.Kind {
	case "sliding":
		if d.PanelFile == "" {
			return scene.DoorSpec{}, fmt.Errorf("sliding door requires panel_file")
		}
		if d.Direction == "" {
			return scene.DoorSpec{}, fmt.Errorf("sliding door requires direction (up, down, left, right)")
		}
		one, err := mergeDoorPanel(s, parentDir, d.PanelFile, params, seen, deps)
		if err != nil {
			return scene.DoorSpec{}, fmt.Errorf("panel_file: %w", err)
		}
		spec.Panels = []scene.DoorPanelGeom{one}
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
	switch spec.Kind {
	case "double":
		if len(spec.Panels) < 2 {
			return fmt.Errorf("kind %q wants 2 panel(s)", spec.Kind)
		}
	case "sliding":
		if len(spec.Panels) < 1 {
			return fmt.Errorf("kind %q wants 1 panel", spec.Kind)
		}
	default:
		if len(spec.Panels) < 1 {
			return fmt.Errorf("kind %q wants 1 panel", spec.Kind)
		}
	}
	for i, p := range spec.Panels {
		if p.PrimaryBox() < 0 {
			return fmt.Errorf("panel[%d] has no box geometry", i)
		}
	}
	return nil
}

func slideDirFromString(dir string) vec.V {
	switch dir {
	case "up":
		return vec.V{Y: 1}
	case "down":
		return vec.V{Y: -1}
	case "left":
		return vec.V{X: -1}
	case "right":
		return vec.V{X: 1}
	default:
		return vec.V{}
	}
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
			if spec.SlideDir.LenSq() > 0 {
				spec.SlideDir = xf.RotateDir(spec.SlideDir).Normalize()
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
