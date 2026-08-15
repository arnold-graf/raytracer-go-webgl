package scene

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestUpdateInstanceTemplate(t *testing.T) {
	world := &Scene{}
	world.Instancing().Templates = []InstanceTemplate{{
		Source: "/objects/desk-anglepoise-lamp.toml",
		Params: map[string]any{"brightness": 0.06},
		Scene:  &Scene{Spheres: []Sphere{{Surface: Surface{Mat: MatDiffuse}}}},
	}}

	updated := &Scene{
		Spheres: []Sphere{
			{Center: vec.New(0, 0.37, 0.235), Radius: 0.024, Surface: Surface{Mat: MatGlass}},
		},
	}
	if !world.UpdateInstanceTemplate("/objects/desk-anglepoise-lamp.toml", map[string]any{"brightness": 0.06}, updated) {
		t.Fatal("expected template update")
	}
	mat, ok := bulbMaterial(world.Instancing().Templates[0].Scene)
	if !ok || mat != MatGlass {
		t.Fatalf("template bulb mat = %v, ok=%v", mat, ok)
	}
}

func TestUpdateInstanceTemplateRequiresExactParams(t *testing.T) {
	world := &Scene{}
	world.Instancing().Templates = []InstanceTemplate{{
		Source: "/objects/desk-anglepoise-lamp.toml",
		Params: map[string]any{"brightness": 0.06, "range": 5.0},
		Scene:  &Scene{Spheres: []Sphere{{Surface: Surface{Mat: MatDiffuse}}}},
	}}
	updated := &Scene{Spheres: []Sphere{{Surface: Surface{Mat: MatGlass}}}}
	wrongProps := map[string]any{
		"brightness": 0.1,
		"range":      3.5,
		"albedo":     []any{0.1, 0.1, 3.0},
	}
	if world.UpdateInstanceTemplate("/objects/desk-anglepoise-lamp.toml", wrongProps, updated) {
		t.Fatal("should not update template when include props differ")
	}
	if world.Instancing().Templates[0].Scene.Spheres[0].Surface.Mat != MatDiffuse {
		t.Fatal("template scene should be unchanged")
	}
}

func bulbMaterial(s *Scene) (int, bool) {
	if s == nil {
		return 0, false
	}
	for _, sp := range s.Spheres {
		if math.Abs(sp.Center.Y-0.37) < 0.01 && sp.Radius < 0.03 {
			return sp.Surface.Mat, true
		}
	}
	return 0, false
}
