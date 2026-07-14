package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestPickInteractable(t *testing.T) {
	s := &Scene{
		Boxes: []Box{
			{Min: vec.New(-0.5, -0.5, -0.5), Max: vec.New(0.5, 0.5, 0.5)},
			{Min: vec.New(9.5, -0.5, -0.5), Max: vec.New(10.5, 0.5, 0.5)},
		},
		Interactables: []Interactable{
			{Hint: "near", Handler: "a", Range: 5},
			{Hint: "far", Handler: "b", Range: 5},
		},
	}
	s.SetBoxInteract(0, 0)
	s.SetBoxInteract(1, 1)

	rayNear := vec.Ray{Origin: vec.New(0, 0, -3), Dir: vec.New(0, 0, 1)}
	if got := s.PickInteractable(rayNear); got == nil || got.Handler != "a" {
		t.Fatalf("want near handler a, got %v", got)
	}

	rayFar := vec.Ray{Origin: vec.New(10, 0, -3), Dir: vec.New(0, 0, 1)}
	if got := s.PickInteractable(rayFar); got == nil || got.Handler != "b" {
		t.Fatalf("want far handler b, got %v", got)
	}

	rayMiss := vec.Ray{Origin: vec.New(5, 0, -3), Dir: vec.New(0, 0, 1)}
	if got := s.PickInteractable(rayMiss); got != nil {
		t.Fatalf("expected nil between targets, got %v", got)
	}

	rayTooFar := vec.Ray{Origin: vec.New(0, 0, -10), Dir: vec.New(0, 0, 1)}
	s.Interactables[0].Range = 2
	if got := s.PickInteractable(rayTooFar); got != nil {
		t.Fatalf("expected nil beyond use_range, got %v", got)
	}

	// Slightly off-center ray misses the 1 m box but lands inside the pick margin.
	s.Interactables[0].Range = 5
	rayMargin := vec.Ray{Origin: vec.New(0.55, 0, -3), Dir: vec.New(0, 0, 1)}
	if got := s.PickInteractable(rayMargin); got == nil || got.Handler != "a" {
		t.Fatalf("want margin hit on near box, got %v", got)
	}
}
