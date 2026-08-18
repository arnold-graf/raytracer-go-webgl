package joltphys

import (
	"math"
	"os"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestMain(m *testing.M) {
	if err := Init(); err != nil {
		os.Exit(0) // skip all tests when Jolt unavailable
	}
	code := m.Run()
	Shutdown()
	os.Exit(code)
}

func TestWorldFromSimpleScene(t *testing.T) {

	sc := &scene.Scene{
		Boxes: []scene.Box{{
			Min: vec.New(-5, 0, -5),
			Max: vec.New(5, 0.1, 5),
		}},
	}
	cfg := camera.DefaultConfig()
	cfg.JoltPhysics = true

	w, err := NewWorldFromScene(sc, vec.New(0, cfg.EyeHeight, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()

	if w.BodyCount() < 1 {
		t.Fatalf("expected static floor body, got %d", w.BodyCount())
	}
	if h := w.GroundHeight(0, 0, cfg.EyeHeight); h < 0.05 || h > 0.15 {
		t.Fatalf("ground height = %v, want ~0.1", h)
	}
}

func TestWorldHonorsBoxTransform(t *testing.T) {

	xf := scene.NewInstanceTransform(0, 0, 0, vec.New(16, 0, -2))
	sc := &scene.Scene{Boxes: []scene.Box{{
		Min:     vec.New(-1, 0, -1),
		Max:     vec.New(1, 6, 1),
		Surface: scene.Surface{Xform: xf},
	}}}
	cfg := camera.DefaultConfig()

	w, err := NewWorldFromScene(sc, vec.New(0, cfg.EyeHeight, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()

	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45
	if !w.Blocked(16, -2, feetY, headY, r, step) {
		t.Fatal("expected wall to block at world position (16,-2)")
	}
	if w.Blocked(0, 0, feetY, headY, r, step) {
		t.Fatal("origin must be clear (no phantom wall from local coords)")
	}
}

func TestWorldHonorsBoxHole(t *testing.T) {
	xf := scene.NewInstanceTransform(0, 0, 0, vec.New(16, 0, -2))
	sc := &scene.Scene{Boxes: []scene.Box{{
		Min:     vec.New(-4, 0, -0.2),
		Max:     vec.New(4, 6, 0.2),
		Holes:   []scene.AABB{{Min: vec.New(-1, 0, -0.3), Max: vec.New(1, 2.5, 0.3)}},
		Surface: scene.Surface{Xform: xf},
	}}}
	cfg := camera.DefaultConfig()

	w, err := NewWorldFromScene(sc, vec.New(0, cfg.EyeHeight, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()

	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45
	if !w.Blocked(19, -2, feetY, headY, r, step) {
		t.Fatal("solid wall should block at world (19,-2)")
	}
	if w.Blocked(16, -2, feetY, headY, r, step) {
		t.Fatal("door hole should be passable at world (16,-2)")
	}
}

func TestWorldThinIncludedFloor(t *testing.T) {

	xf := scene.NewInstanceTransform(0, 0, 0, vec.New(0, 10, 0))
	sc := &scene.Scene{Boxes: []scene.Box{{
		Min:     vec.New(0, 0, 0),
		Max:     vec.New(10, 0.01, 10),
		Surface: scene.Surface{Xform: xf},
	}}}
	cfg := camera.DefaultConfig()

	w, err := NewWorldFromScene(sc, vec.New(5, cfg.EyeHeight+10, 5), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()

	if h := w.GroundHeight(5, 5, cfg.EyeHeight+12); math.Abs(h-10.01) > 0.08 {
		t.Fatalf("floor top = %v, want ~10.01", h)
	}
}
