package app

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

const (
	flashlightAnimSec   = 0.35
	flashlightHiddenUp  = -0.28
	flashlightRaisedUp  = -0.07
	flashlightRight     = 0.11
	flashlightForward   = 0.06
	flashlightRelPath   = "scenes/objects/flashlight.toml"
)

// Flashlight is a camera-attached handheld torch loaded from flashlight.toml.
type Flashlight struct {
	shown   bool
	t       float64
	mounted bool
	body    scene.DynamicBody
	tmpl    *scene.Scene
	light   scene.Light
}

// Init loads flashlight.toml into memory. Geometry and the beam are not added to
// the scene until the player activates it (F).
func (fl *Flashlight) Init() error {
	fl.shown = false
	fl.t = 0
	fl.mounted = false
	fl.body = scene.DynamicBody{}
	tmpl, err := loadFlashlightTemplate()
	fl.tmpl = tmpl
	if err == nil && tmpl != nil && len(tmpl.Lights) > 0 {
		fl.light = tmpl.Lights[0]
	}
	return err
}

func loadFlashlightTemplate() (*scene.Scene, error) {
	candidates := []string{flashlightRelPath}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, flashlightRelPath))
	}
	for _, p := range candidates {
		s, err := sceneio.Load(p)
		if err == nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("load %s: not found (run from repo root)", flashlightRelPath)
}

func (fl *Flashlight) refreshTemplate() error {
	tmpl, err := loadFlashlightTemplate()
	if err != nil {
		return err
	}
	fl.tmpl = tmpl
	if len(tmpl.Lights) > 0 {
		fl.light = tmpl.Lights[0]
	}
	return nil
}

func (fl *Flashlight) spawn(sc *scene.Scene) error {
	if fl.mounted || sc == nil {
		return nil
	}
	if err := fl.refreshTemplate(); err != nil {
		return err
	}
	if fl.tmpl == nil {
		return nil
	}
	cylStart := len(sc.Cylinders)
	lightStart := len(sc.Lights)
	sc.Cylinders = append(sc.Cylinders, fl.tmpl.Cylinders...)
	if len(fl.tmpl.Lights) > 0 {
		sc.Lights = append(sc.Lights, fl.light)
	}
	fl.body = scene.DynamicBody{
		Name:      "flashlight",
		Cylinders: [2]int{cylStart, len(sc.Cylinders)},
		Lights:    [2]int{lightStart, len(sc.Lights)},
	}
	sc.DynamicBodies = append(sc.DynamicBodies, fl.body)
	fl.mounted = true
	sc.Touch()
	return nil
}

func (fl *Flashlight) despawn(sc *scene.Scene) {
	if !fl.mounted || sc == nil {
		return
	}
	sc.RemoveDynamicBody(fl.body)
	fl.body = scene.DynamicBody{}
	fl.mounted = false
	sc.Touch()
}

func (fl *Flashlight) remountIfStale(sc *scene.Scene) {
	if !fl.mounted || sc == nil {
		return
	}
	if fl.body.Attached(sc) {
		return
	}
	wasShown := fl.shown
	fl.mounted = false
	fl.body = scene.DynamicBody{}
	if wasShown {
		_ = fl.spawn(sc)
	}
}

// Toggle raises or lowers the flashlight (F key).
func (fl *Flashlight) Toggle() { fl.shown = !fl.shown }

// Update animates pose and follows the camera.
func (fl *Flashlight) Update(sc *scene.Scene, cam *camera.Camera, dt float64) bool {
	if sc == nil || cam == nil {
		return false
	}
	if fl.shown && !fl.mounted {
		_ = fl.spawn(sc)
	}
	if !fl.mounted {
		return false
	}
	fl.remountIfStale(sc)
	if !fl.mounted {
		return false
	}

	target := 0.0
	if fl.shown {
		target = 1.0
	}
	speed := 1.0 / flashlightAnimSec
	prev := fl.t
	if fl.t < target {
		fl.t = math.Min(target, fl.t+speed*dt)
	} else if fl.t > target {
		fl.t = math.Max(target, fl.t-speed*dt)
	}
	fl.applyPose(sc, cam)
	changed := fl.t != prev

	if !fl.shown && fl.t <= 0 {
		fl.despawn(sc)
		return true
	}
	sc.TouchTransforms()
	return changed
}

func flashlightEase(t float64) float64 {
	return t * t * (3 - 2*t)
}

func (fl *Flashlight) applyPose(sc *scene.Scene, cam *camera.Camera) {
	fwd, right, up := cam.Basis()
	e := flashlightEase(fl.t)
	origin := cam.Pos.
		Add(fwd.Scale(flashlightForward * e)).
		Add(right.Scale(flashlightRight * e)).
		Add(up.Scale(flashlightHiddenUp + (flashlightRaisedUp-flashlightHiddenUp)*e))
	root := scene.NewTransformYZ(origin, fwd, up)

	for i := fl.body.Cylinders[0]; i < fl.body.Cylinders[1]; i++ {
		sc.Cylinders[i].Xform = root
	}

	if fl.body.Lights[1] <= fl.body.Lights[0] {
		return
	}
	beam := fl.light
	beam.Pos = root.ToWorld(beam.Pos)
	if beam.IsSpot() {
		beam.Dir = root.RotateDir(beam.Dir)
	}
	scale := e
	beam.Color = vec.V{
		X: fl.light.Color.X * scale,
		Y: fl.light.Color.Y * scale,
		Z: fl.light.Color.Z * scale,
	}
	sc.Lights[fl.body.Lights[0]] = beam
}
