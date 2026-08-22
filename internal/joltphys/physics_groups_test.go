package joltphys

import (
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestWorldSpawnsPhysicsGroups(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{{
			Min:     vec.New(-5, 0, -5),
			Max:     vec.New(5, 0.1, 5),
			Surface: scene.Surface{Mat: scene.MatDiffuse},
		}, {
			Min:     vec.New(-0.15, 0.1, -0.15),
			Max:     vec.New(0.15, 0.4, 0.15),
			Surface: scene.Surface{Mat: scene.MatDiffuse},
		}},
		PhysicsGroups: []scene.PhysicsGroup{{
			Name: "crate",
			Spec: scene.PhysicsSpec{
				Mode:   scene.PhysicsCompound,
				MassKg: 2.0,
				Sleep:  true,
			},
			Body: scene.DynamicBody{Name: "crate", Boxes: [2]int{1, 2}},
		}},
		DynamicBodies: []scene.DynamicBody{{Name: "crate", Boxes: [2]int{1, 2}}},
	}
	cfg := camera.DefaultConfig()
	cfg.JoltPhysics = true

	w, err := NewWorldFromScene(sc, vec.New(0, cfg.EyeHeight, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()

	if w.BodyCount() < 2 {
		t.Fatalf("expected floor + dynamic crate, got %d bodies", w.BodyCount())
	}
	if len(w.bindings.bindings) != 1 {
		t.Fatalf("bindings = %d, want 1 dynamic", len(w.bindings.bindings))
	}
}
