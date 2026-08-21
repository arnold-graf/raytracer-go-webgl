package webgpu

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestDefaultGlassIsThin(t *testing.T) {
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
		t.Fatal("glass should pack thin flag by default")
	}
}

func TestThickGlassOmitsThinFlag(t *testing.T) {
	s := &scene.Scene{
		Boxes: []scene.Box{{
			Min: vec.V{},
			Max: vec.V{X: 1, Y: 2, Z: 0.1},
			Surface: scene.Surface{
				Mat:  scene.MatGlass,
				Thin: false,
			},
		}},
	}
	prims := PackPrimitives(s)
	if prims[0].Meta[3]&primFlagGlassThin != 0 {
		t.Fatal("thick glass should not set thin flag")
	}
}

func TestTwoPaneOmitsThinFlag(t *testing.T) {
	s := &scene.Scene{
		Boxes: []scene.Box{{
			Min: vec.V{},
			Max: vec.V{X: 1, Y: 2, Z: 0.1},
			Surface: scene.Surface{
				Mat:     scene.MatGlass,
				Thin:    false,
				TwoPane: true,
			},
		}},
	}
	prims := PackPrimitives(s)
	if prims[0].Meta[3]&primFlagGlassThin != 0 {
		t.Fatal("two_pane should use thick glass without thin flag")
	}
}
