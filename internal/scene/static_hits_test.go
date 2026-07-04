package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestStaticHitsIgnoresFaceTouch(t *testing.T) {
	s := &Scene{
		Boxes: []Box{
			{Min: vec.V{}, Max: vec.V{X: 1, Y: 1, Z: 1}},
			{Min: vec.V{X: 1, Y: 0, Z: 0}, Max: vec.V{X: 2, Y: 1, Z: 1}},
		},
	}
	hits := s.StaticHits(vec.V{}, vec.V{X: 1, Y: 1, Z: 1}, func(i int) bool { return i == 0 })
	if len(hits) != 0 {
		t.Fatalf("face touch should not register, got %v", hits)
	}
	hits = s.StaticHits(vec.V{}, vec.V{X: 1.1, Y: 1, Z: 1}, func(i int) bool { return i == 0 })
	if len(hits) != 1 || hits[0].Box != 1 {
		t.Fatalf("penetrating overlap = %v", hits)
	}
}

func TestStaticHitsSkipsDynamicCylinder(t *testing.T) {
	s := &Scene{
		Cylinders: []Cylinder{{CX: 0.5, CZ: 0.5, YMin: 0, YMax: 2, Radius: 0.5}},
		DynamicBodies: []DynamicBody{{
			Name:      "npc",
			Cylinders: [2]int{0, 1},
		}},
	}
	hits := s.StaticHits(vec.V{}, vec.V{X: 1, Y: 2, Z: 1}, nil)
	if len(hits) != 0 {
		t.Fatalf("dynamic cylinder should be skipped, got %v", hits)
	}
}
