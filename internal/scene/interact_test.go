package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestMergeInteractablesReindexes(t *testing.T) {
	dst := &Scene{
		Interactables: []Interactable{{Hint: "first", Handler: "a"}},
	}
	sub := &Scene{
		Interactables: []Interactable{
			{Hint: "lamp", Handler: "state", StateAction: "toggle(is_on)"},
		},
	}
	sub.SetSphereInteract(0, 0)
	sub.Spheres = []Sphere{{Center: vec.New(0, 0.37, 0.235), Radius: 0.024}}
	dst.Spheres = append(dst.Spheres, sub.Spheres...)
	dst.MergeInteractables(sub, 0)

	if dst.Interactables[0].Index() != 0 {
		t.Fatalf("first Index() = %d, want 0", dst.Interactables[0].Index())
	}
	if dst.Interactables[1].Index() != 1 {
		t.Fatalf("merged Index() = %d, want 1", dst.Interactables[1].Index())
	}
	if sphereIdx, ok := dst.InteractableSphereIndex(1); !ok || sphereIdx != 0 {
		t.Fatalf("sphereInteract[1] = %d, ok=%v, want 0 true", sphereIdx, ok)
	}
}

func TestApplyInteractBindingsInSpan(t *testing.T) {
	dst := &Scene{}
	local := &Scene{
		Spheres: []Sphere{
			{Center: vec.New(0, 0.30, -0.14), Radius: 0.03},
			{Center: vec.New(0, 0.37, 0.235), Radius: 0.024},
		},
		Interactables: []Interactable{{Hint: "lamp", Handler: "state"}},
	}
	local.Interactables[0].index = 0
	local.SetSphereInteract(1, 0)

	dst.Spheres = append(dst.Spheres, local.Spheres...)
	dst.Interactables = append(dst.Interactables, local.Interactables...)
	dst.Interactables[0].index = 0

	iaSpan := [2]int{0, 1}
	dst.ApplyInteractBindings(local, InteractBindingOffsets{
		Spheres:       0,
		Interactables: 0,
	}, &iaSpan)

	if sphereIdx, ok := dst.InteractableSphereIndex(0); !ok || sphereIdx != 1 {
		t.Fatalf("sphereInteract = %d, ok=%v, want 1 true", sphereIdx, ok)
	}
}

func TestPickInteractableSphere(t *testing.T) {
	s := &Scene{
		Spheres: []Sphere{
			{Center: vec.New(0, 0, 0), Radius: 0.05},
		},
		Interactables: []Interactable{
			{Hint: "bulb", Handler: "state", StateAction: "toggle(on)"},
		},
	}
	s.SetSphereInteract(0, 0)

	ray := vec.Ray{Origin: vec.New(0, 0, -1), Dir: vec.New(0, 0, 1)}
	if got := s.PickInteractable(ray); got == nil || got.Handler != "state" || got.SphereIndex != 0 {
		t.Fatalf("pick = %+v", got)
	}
}

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
