package app

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

const (
	torchAnimSec   = 0.35
	torchHiddenUp  = -0.38 // lowered: further below the eyes
	torchRaisedUp  = -0.22 // raised: lower-right of the view
	torchRight     = 0.16
	torchForward   = 0.32 // in front of the camera so the upright stick is visible
	torchRelPath   = "scenes/objects/torch.toml"
)

// Torch is a camera-attached handheld burning torch loaded from torch.toml.
type Torch struct {
	shown          bool
	t              float64
	mounted        bool
	body           scene.DynamicBody
	tmpl           *scene.Scene
	campfire       scene.Campfire
	localCenter    vec.V
	baseBrightness float64
	srcPath        string
	srcMod         time.Time
}

// Init loads torch.toml into memory. Geometry and the flame are not added to
// the scene until the player activates it (F).
func (t *Torch) Init() error {
	t.shown = false
	t.t = 0
	t.mounted = false
	t.body = scene.DynamicBody{}
	tmpl, path, err := loadTorchTemplate()
	t.tmpl = tmpl
	t.srcPath = path
	if err == nil {
		if mt, ok := fileModTime(path); ok {
			t.srcMod = mt
		}
		t.applyCampfireFromTemplate()
	}
	return err
}

func resolveTorchPath() (string, error) {
	candidates := []string{torchRelPath}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, torchRelPath))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			abs, err := filepath.Abs(p)
			if err != nil {
				return p, nil
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("load %s: not found (run from repo root)", torchRelPath)
}

func loadTorchTemplate() (*scene.Scene, string, error) {
	path, err := resolveTorchPath()
	if err != nil {
		return nil, "", err
	}
	s, err := sceneio.Load(path)
	if err != nil {
		return nil, path, err
	}
	return s, path, nil
}

func (t *Torch) applyCampfireFromTemplate() {
	if t.tmpl == nil || len(t.tmpl.Campfires) == 0 {
		return
	}
	t.campfire = t.tmpl.Campfires[0]
	t.localCenter = t.campfire.Center
	t.baseBrightness = t.campfire.Brightness
	if t.baseBrightness == 0 {
		t.baseBrightness = 1
	}
}

// ReloadIfDirty reloads torch.toml when the file changes on disk. Returns true
// when a new template was applied. A failed parse keeps the current template.
func (t *Torch) ReloadIfDirty(sc *scene.Scene) (bool, error) {
	if t.srcPath == "" {
		return false, nil
	}
	cur, ok := fileModTime(t.srcPath)
	if !ok || !cur.After(t.srcMod) {
		return false, nil
	}
	if err := t.reload(sc); err != nil {
		return false, err
	}
	t.srcMod = cur
	return true, nil
}

func (t *Torch) reload(sc *scene.Scene) error {
	tmpl, path, err := loadTorchTemplate()
	if err != nil {
		return err
	}
	t.tmpl = tmpl
	t.srcPath = path
	t.applyCampfireFromTemplate()
	return t.remount(sc)
}

func (t *Torch) remount(sc *scene.Scene) error {
	if !t.mounted || sc == nil {
		return nil
	}
	shown := t.shown
	anim := t.t
	t.despawn(sc)
	t.shown = shown
	t.t = anim
	return t.spawn(sc)
}

func (t *Torch) refreshTemplate() error {
	tmpl, path, err := loadTorchTemplate()
	if err != nil {
		return err
	}
	t.tmpl = tmpl
	t.srcPath = path
	t.applyCampfireFromTemplate()
	return nil
}

func (t *Torch) spawn(sc *scene.Scene) error {
	if t.mounted || sc == nil {
		return nil
	}
	if err := t.refreshTemplate(); err != nil {
		return err
	}
	if t.tmpl == nil {
		return nil
	}
	cylStart := len(sc.Cylinders)
	campfireStart := len(sc.Campfires)
	sc.Cylinders = append(sc.Cylinders, t.tmpl.Cylinders...)
	if len(t.tmpl.Campfires) > 0 {
		sc.Campfires = append(sc.Campfires, t.campfire)
	}
	t.body = scene.DynamicBody{
		Name:      "torch",
		Cylinders: [2]int{cylStart, len(sc.Cylinders)},
		Campfires: [2]int{campfireStart, len(sc.Campfires)},
	}
	sc.DynamicBodies = append(sc.DynamicBodies, t.body)
	t.mounted = true
	sc.Touch()
	return nil
}

func (t *Torch) despawn(sc *scene.Scene) {
	if !t.mounted || sc == nil {
		return
	}
	sc.RemoveDynamicBody(t.body)
	t.body = scene.DynamicBody{}
	t.mounted = false
	sc.Touch()
}

func (t *Torch) remountIfStale(sc *scene.Scene) {
	if !t.mounted || sc == nil {
		return
	}
	if t.body.Attached(sc) {
		return
	}
	wasShown := t.shown
	t.mounted = false
	t.body = scene.DynamicBody{}
	if wasShown {
		_ = t.spawn(sc)
	}
}

// Toggle raises or lowers the torch (F key).
func (t *Torch) Toggle() { t.shown = !t.shown }

// Update animates pose and follows the camera.
func (t *Torch) Update(sc *scene.Scene, cam *camera.Camera, dt float64) bool {
	if sc == nil || cam == nil {
		return false
	}
	if t.shown && !t.mounted {
		_ = t.spawn(sc)
	}
	if !t.mounted {
		return false
	}
	t.remountIfStale(sc)
	if !t.mounted {
		return false
	}

	target := 0.0
	if t.shown {
		target = 1.0
	}
	speed := 1.0 / torchAnimSec
	prev := t.t
	if t.t < target {
		t.t = math.Min(target, t.t+speed*dt)
	} else if t.t > target {
		t.t = math.Max(target, t.t-speed*dt)
	}
	t.applyPose(sc, cam)
	changed := t.t != prev

	if !t.shown && t.t <= 0 {
		t.despawn(sc)
		return true
	}
	sc.TouchTransforms()
	return changed
}

func torchEase(t float64) float64 {
	return t * t * (3 - 2*t)
}

func (t *Torch) applyPose(sc *scene.Scene, cam *camera.Camera) {
	fwd, right, up := cam.Basis()
	e := torchEase(t.t)
	origin := cam.Pos.
		Add(fwd.Scale(torchForward * e)).
		Add(right.Scale(torchRight * e)).
		Add(up.Scale(torchHiddenUp + (torchRaisedUp-torchHiddenUp)*e))
	root := scene.NewTransformYZ(origin, up, fwd)

	for i := t.body.Cylinders[0]; i < t.body.Cylinders[1]; i++ {
		sc.Cylinders[i].Xform = root
	}

	if t.body.Campfires[1] <= t.body.Campfires[0] {
		return
	}
	cf := t.campfire
	cf.Center = root.ToWorld(t.localCenter)
	bright := t.baseBrightness * e
	if bright < 0.01 {
		bright = 0.01
	}
	cf.Brightness = bright
	sc.Campfires[t.body.Campfires[0]] = cf
}
