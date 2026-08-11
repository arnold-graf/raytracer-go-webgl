package sceneio

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestArtDecoDoorFrameLegsGrounded(t *testing.T) {
	s, err := LoadIncludeSubScene("../../scenes/office-sunset/objects/art-deco-door-frame.toml", map[string]any{
		"width": 8.0, "height": 4.5, "depth": 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var nearMinY, farMinY float64 = 1e9, 1e9
	for _, b := range s.Boxes {
		bmin, max := b.WorldBounds()
		if bmin.Y > 0.05 {
			continue
		}
		cz := (bmin.Z + max.Z) / 2
		if cz > 4 {
			if bmin.Y < farMinY {
				farMinY = bmin.Y
			}
		} else {
			if bmin.Y < nearMinY {
				nearMinY = bmin.Y
			}
		}
	}
	t.Logf("width=8 nearZ leg minY=%.4f farZ leg minY=%.4f", nearMinY, farMinY)
	if nearMinY > 0.05 {
		t.Errorf("near leg minY=%.4f, want 0", nearMinY)
	}
	if farMinY < -0.05 {
		t.Errorf("far leg minY=%.4f, want >= 0", farMinY)
	}
}

func TestArtDecoDoorFrameInAtrium(t *testing.T) {
	s, err := Load("../../scenes/office-sunset/office-atrium.toml")
	if err != nil {
		t.Fatal(err)
	}
	var westMinX, eastMinX float64 = 1e9, 1e9
	for _, b := range s.Boxes {
		bmin, bmax := b.WorldBounds()
		if bmax.Y-bmin.Y < 0.15 || bmin.Y > 0.1 {
			continue
		}
		cx := (bmin.X + bmax.X) / 2
		cz := (bmin.Z + bmax.Z) / 2
		if cz < 0 || cz > 1 || cx < 19 || cx > 32 {
			continue
		}
		if cx < 25 {
			if bmin.X < westMinX {
				westMinX = bmin.X
			}
		} else {
			if bmin.X < eastMinX {
				eastMinX = bmin.X
			}
		}
	}
	t.Logf("atrium door west leg minX=%.3f east leg minX=%.3f", westMinX, eastMinX)
	if westMinX < 21.0 {
		t.Errorf("west leg minX=%.3f, want >= 21 (inside hole)", westMinX)
	}
	if eastMinX < 29.0 {
		t.Errorf("east leg minX=%.3f, want >= 29 (inside hole)", eastMinX)
	}
}

func TestArtDecoDoorFrameInServerRoom(t *testing.T) {
	s, err := Load("../../scenes/office-sunset/server-room-1.toml")
	if err != nil {
		t.Fatal(err)
	}
	var southMinX, northMinX float64 = 1e9, 1e9
	for _, b := range s.Boxes {
		bmin, bmax := b.WorldBounds()
		if bmax.Y-bmin.Y < 0.15 || bmin.Y > 0.1 {
			continue
		}
		cx := (bmin.X + bmax.X) / 2
		cz := (bmin.Z + bmax.Z) / 2
		if cx < 38 || cx > 41 || cz < 7.5 || cz > 13.5 {
			continue
		}
		if cz < 10.5 {
			if bmin.X < southMinX {
				southMinX = bmin.X
			}
		} else {
			if bmin.X < northMinX {
				northMinX = bmin.X
			}
		}
	}
	t.Logf("server-room door south leg minX=%.3f north leg minX=%.3f", southMinX, northMinX)
	if southMinX > 40.0 || northMinX > 40.0 {
		t.Errorf("legs minX=%.3f/%.3f protrude into glass at x=40", southMinX, northMinX)
	}
}

func TestArtDecoLeftLegTransformOrigin(t *testing.T) {
	// Z offset in transform_origin becomes a Y lift after rotate_x=90.
	broken := scene.PlacementTransform(90, 180, 0, vec.New(0, 0, 8), vec.New(1, 0, 7))
	if broken.ToWorld(vec.V{}).Y < 6.9 {
		t.Fatalf("broken pivot should lift leg, got Y=%.3f", broken.ToWorld(vec.V{}).Y)
	}
	fixed := scene.PlacementTransform(-90, 180, 0, vec.New(0, 0, 8), vec.New(1, 0, 0))
	if fixed.ToWorld(vec.V{}).Y > 0.01 {
		t.Errorf("fixed pivot bottom Y=%.4f, want 0", fixed.ToWorld(vec.V{}).Y)
	}
}
