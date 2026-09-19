package webgpu

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// Shape.w carries the exclusion to the shader. It is the last free lane of the
// Light struct, so a drift here silently re-enables every suppressed highlight.
func TestPackLightCarriesNoSpecular(t *testing.T) {
	base := scene.Light{Pos: vec.New(0, 1, 0), Color: vec.New(1, 1, 1), Range: 10}
	if got := packLight(&base).Shape[3]; got != 0 {
		t.Errorf("default light packs Shape.w = %v, want 0 (specular on)", got)
	}
	off := base
	off.NoSpecular = true
	if got := packLight(&off).Shape[3]; got != 1 {
		t.Errorf("NoSpecular light packs Shape.w = %v, want 1", got)
	}
	// The other three lanes must be untouched by it.
	a, b := packLight(&base), packLight(&off)
	if a.Shape[0] != b.Shape[0] || a.Shape[1] != b.Shape[1] || a.Shape[2] != b.Shape[2] {
		t.Errorf("NoSpecular disturbed the other Shape lanes: %v vs %v", a.Shape, b.Shape)
	}
}
