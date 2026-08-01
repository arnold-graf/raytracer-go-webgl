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
	spyglassAnimSec     = 0.5
	spyglassEyeBack     = 0.020 // camera local Y when raised
	spyglassHiddenUp    = -0.2
	spyglassRaisedUp    = 0.0
	spyglassBounceDepth = 6
	spyglassRelPath     = "scenes/objects/spyglass.toml"
	// Matte slab behind the camera (camera-local −Y) to block rear reflections in the lens.
	spyglassBodyBack  = 0.03
	spyglassBodyThick = 0.025
	spyglassBodyHalfW = 0.38
	spyglassBodyHalfH = 0.42
)

// Spyglass is a camera-attached handheld viewer loaded from spyglass.toml.
type Spyglass struct {
	shown   bool
	t       float64
	mounted bool
	body    scene.DynamicBody
	tmpl    *scene.Scene
}

// MaxBounceDepth returns the tracer recursion cap while the spyglass is visible.
func (sg *Spyglass) MaxBounceDepth() uint32 {
	if !sg.mounted || sg.t <= 0 {
		return 0
	}
	return spyglassBounceDepth
}

// Init loads spyglass.toml into memory. Geometry is not added to the scene
// until the player activates it (Q).
func (sg *Spyglass) Init() error {
	sg.shown = false
	sg.t = 0
	sg.mounted = false
	sg.body = scene.DynamicBody{}
	tmpl, err := loadSpyglassTemplate()
	sg.tmpl = tmpl
	return err
}

func loadSpyglassTemplate() (*scene.Scene, error) {
	candidates := []string{spyglassRelPath}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, spyglassRelPath))
	}
	for _, p := range candidates {
		s, err := sceneio.Load(p)
		if err == nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("load %s: not found (run from repo root)", spyglassRelPath)
}

func (sg *Spyglass) spawn(sc *scene.Scene) error {
	if sg.mounted || sc == nil || sg.tmpl == nil {
		return nil
	}
	boxStart := len(sc.Boxes)
	cylStart := len(sc.Cylinders)
	lensStart := len(sc.Lenses)
	sc.Boxes = append(sc.Boxes, spyglassBodyBlocker())
	sc.Cylinders = append(sc.Cylinders, sg.tmpl.Cylinders...)
	sc.Lenses = append(sc.Lenses, sg.tmpl.Lenses...)
	sg.body = scene.DynamicBody{
		Name:      "spyglass",
		Boxes:     [2]int{boxStart, len(sc.Boxes)},
		Cylinders: [2]int{cylStart, len(sc.Cylinders)},
		Lenses:    [2]int{lensStart, len(sc.Lenses)},
	}
	sc.DynamicBodies = append(sc.DynamicBodies, sg.body)
	sg.mounted = true
	sc.Touch()
	return nil
}

func (sg *Spyglass) despawn(sc *scene.Scene) {
	if !sg.mounted || sc == nil {
		return
	}
	sc.RemoveDynamicBody(sg.body)
	sg.body = scene.DynamicBody{}
	sg.mounted = false
	sc.Touch()
}

func (sg *Spyglass) remountIfStale(sc *scene.Scene) {
	if !sg.mounted || sc == nil {
		return
	}
	if sg.body.Attached(sc) {
		return
	}
	wasShown := sg.shown
	sg.mounted = false
	sg.body = scene.DynamicBody{}
	if wasShown {
		_ = sg.spawn(sc)
	}
}

// Toggle raises or lowers the spyglass (Q key).
func (sg *Spyglass) Toggle() { sg.shown = !sg.shown }

// Update animates pose and follows the camera.
func (sg *Spyglass) Update(sc *scene.Scene, cam *camera.Camera, dt float64) bool {
	if sc == nil || cam == nil {
		return false
	}
	if sg.shown && !sg.mounted {
		_ = sg.spawn(sc)
	}
	if !sg.mounted {
		return false
	}
	sg.remountIfStale(sc)
	if !sg.mounted {
		return false
	}

	target := 0.0
	if sg.shown {
		target = 1.0
	}
	speed := 1.0 / spyglassAnimSec
	prev := sg.t
	if sg.t < target {
		sg.t = math.Min(target, sg.t+speed*dt)
	} else if sg.t > target {
		sg.t = math.Max(target, sg.t-speed*dt)
	}
	sg.applyPose(sc, cam)
	changed := sg.t != prev

	if !sg.shown && sg.t <= 0 {
		sg.despawn(sc)
		return true
	}
	sc.TouchTransforms()
	return changed
}

func spyglassEase(t float64) float64 {
	return t * t * (3 - 2*t)
}

func (sg *Spyglass) applyPose(sc *scene.Scene, cam *camera.Camera) {
	fwd, _, up := cam.Basis()
	e := spyglassEase(sg.t)
	origin := cam.Pos.
		Sub(fwd.Scale(spyglassEyeBack * e)).
		Add(up.Scale(spyglassHiddenUp + (spyglassRaisedUp-spyglassHiddenUp)*e))
	root := scene.NewTransformYZ(origin, fwd, up)
	camRoot := scene.NewTransformYZ(cam.Pos, fwd, up)

	for i := sg.body.Boxes[0]; i < sg.body.Boxes[1]; i++ {
		sc.Boxes[i].Xform = camRoot
	}
	for i := sg.body.Cylinders[0]; i < sg.body.Cylinders[1]; i++ {
		sc.Cylinders[i].Xform = root
	}
	for i := sg.body.Lenses[0]; i < sg.body.Lenses[1]; i++ {
		sc.Lenses[i].Xform = root
	}
}

func spyglassBodyBlocker() scene.Box {
	yNear := -spyglassBodyBack
	yFar := yNear - spyglassBodyThick
	return scene.Box{
		Min: vec.New(-spyglassBodyHalfW, yFar, -spyglassBodyHalfH),
		Max: vec.New(spyglassBodyHalfW, yNear, spyglassBodyHalfH),
		Surface: scene.Surface{
			Mat:         scene.MatDiffuse,
			Albedo:      vec.V{X: 0.02, Y: 0.02, Z: 0.02},
			IOR:         1.5,
			NoCollision: true,
		},
	}
}
