package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestStaticHitsRespectsBoxHoles(t *testing.T) {
	// Wall with a door-sized opening; a probe filling only the hole must not hit.
	s := &Scene{
		Boxes: []Box{{
			Min: vec.New(0, 0, 0),
			Max: vec.New(4, 4, 0.5),
			Holes: []AABB{{
				Min: vec.New(1, 0, -0.1),
				Max: vec.New(3, 2.5, 0.6),
			}},
		}},
	}
	// Probe entirely inside the hole.
	hits := s.StaticHits(vec.New(1.1, 0.1, 0), vec.New(2.9, 2.4, 0.5), nil)
	if len(hits) != 0 {
		t.Fatalf("probe in hole should not hit wall, got %v", hits)
	}
	// Probe in solid lintel above the hole.
	hits = s.StaticHits(vec.New(1.1, 2.6, 0), vec.New(2.9, 3.9, 0.5), nil)
	if len(hits) != 1 || hits[0].Box != 0 {
		t.Fatalf("probe in lintel should hit wall, got %v", hits)
	}
}

func TestStaticHitsHoledBoxWithTransform(t *testing.T) {
	s := &Scene{
		Boxes: []Box{{
			Min:     vec.New(-1, 0, -0.3),
			Max:     vec.New(1, 2.5, 0.3),
			Holes:   []AABB{{Min: vec.New(-0.9, 0.1, -0.35), Max: vec.New(0.9, 2.4, 0.35)}},
			Surface: Surface{Xform: NewRigidTransform(0, 90, 0, vec.New(10, 0, 5))},
		}},
	}
	// Door panel probe at world opening (include-style placement).
	hits := s.StaticHits(vec.New(9.7, 0.2, 4.1), vec.New(10.3, 2.3, 4.9), nil)
	if len(hits) != 0 {
		t.Fatalf("probe in transformed hole should not hit, got %v", hits)
	}
}

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
