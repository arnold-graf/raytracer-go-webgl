package webgpu

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestThinGlassFlagPacked(t *testing.T) {
	s := &scene.Scene{
		Boxes: []scene.Box{{
			Min: vec.V{},
			Max: vec.V{X: 1, Y: 2, Z: 0.1},
			Surface: scene.Surface{
				Mat:  scene.MatGlass,
				Thin: true,
			},
		}},
	}
	prims := PackPrimitives(s)
	if len(prims) != 1 {
		t.Fatalf("prims = %d, want 1", len(prims))
	}
	if prims[0].Meta[3]&primFlagGlassThin == 0 {
		t.Fatal("thin glass flag not packed")
	}
}

func TestThickGlassOmitsThinFlag(t *testing.T) {
	s := &scene.Scene{
		Boxes: []scene.Box{{
			Min: vec.V{},
			Max: vec.V{X: 1, Y: 2, Z: 0.1},
			Surface: scene.Surface{
				Mat: scene.MatGlass,
			},
		}},
	}
	prims := PackPrimitives(s)
	if prims[0].Meta[3]&primFlagGlassThin != 0 {
		t.Fatal("thick glass should not set thin flag")
	}
}
