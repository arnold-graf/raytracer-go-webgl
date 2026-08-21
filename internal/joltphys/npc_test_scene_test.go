package joltphys

import (
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestWorldLargeFloorBox(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{{
			Min: vec.New(-8, 0, -8),
			Max: vec.New(8, 0.1, 8),
		}},
	}
	cfg := camera.DefaultConfig()
	w, err := NewWorldFromScene(sc, vec.New(0, cfg.EyeHeight, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()
}

func TestWorldFromNPCTestScene(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/npc-test.toml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := camera.DefaultConfig()
	cfg.JoltPhysics = true
	w, err := NewWorldFromScene(sc, vec.New(0, cfg.EyeHeight, 5), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()
	if w.BodyCount() < 1 {
		t.Fatalf("expected bodies, got %d", w.BodyCount())
	}
}
