package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestNearestInteractable(t *testing.T) {
	s := &Scene{
		Interactables: []Interactable{
			{Hint: "near", Handler: "a", Range: 2, Center: vec.New(0, 0, 0)},
			{Hint: "far", Handler: "b", Range: 1, Center: vec.New(10, 0, 0)},
		},
	}
	if got := s.NearestInteractable(vec.New(0.5, 0, 0)); got == nil || got.Handler != "a" {
		t.Fatalf("want near handler a, got %v", got)
	}
	if got := s.NearestInteractable(vec.New(5, 0, 0)); got != nil {
		t.Fatalf("expected nil between targets, got %v", got)
	}
}
