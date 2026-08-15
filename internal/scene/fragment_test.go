package scene

import (
	"testing"

	"raytracer/internal/vec"
)

func TestRemapLightReferencesAfterSplice(t *testing.T) {
	sc := &Scene{
		Lights: make([]Light, 12),
		Interactables: []Interactable{
			{Hint: "switch", LightIndex: -1},
			{Hint: "uplight-a", LightIndex: 10},
			{Hint: "uplight-b", LightIndex: 11},
		},
	}
	sc.Interactables[0].index = 0
	sc.Interactables[1].index = 1
	sc.Interactables[2].index = 2
	sc.SetLightInteract(10, 1)
	sc.SetLightInteract(11, 2)

	remapLightReferencesAfter(sc, [2]int{2, 10}, -8)

	if _, ok := sc.LightInteractIndex(10); ok {
		t.Fatal("stale lightInteract key 10 should be gone")
	}
	if ia, ok := sc.LightInteractIndex(2); !ok || ia != 1 {
		t.Fatalf("lightInteract[2] = %d, ok=%v, want 1 true", ia, ok)
	}
	if ia, ok := sc.LightInteractIndex(3); !ok || ia != 2 {
		t.Fatalf("lightInteract[3] = %d, ok=%v, want 2 true", ia, ok)
	}
	if sc.Interactables[1].LightIndex != 2 || sc.Interactables[2].LightIndex != 3 {
		t.Fatalf("LightIndex = %d,%d, want 2,3", sc.Interactables[1].LightIndex, sc.Interactables[2].LightIndex)
	}
}

func TestSpliceFragmentPreservesSphereInteractMap(t *testing.T) {
	dst := &Scene{
		Spheres: []Sphere{
			{Center: vecFrom(0, 0.30, -0.14), Radius: 0.03},
			{Center: vecFrom(0, 0.46, 0.22), Radius: 0.016},
			{Center: vecFrom(0, 0.37, 0.235), Radius: 0.024},
		},
		Lights: []Light{{Pos: vecFrom(0, 0.36, 0.235), Radius: 0.12}},
		Interactables: []Interactable{
			{Hint: "lamp", Handler: "state", StateAction: "toggle(is_on)"},
		},
	}
	dst.Interactables[0].index = 0
	dst.SetSphereInteract(2, 0)

	local := &Scene{
		Spheres: []Sphere{
			{Center: vecFrom(0, 0.30, -0.14), Radius: 0.03},
			{Center: vecFrom(0, 0.46, 0.22), Radius: 0.016},
			{Center: vecFrom(0, 0.37, 0.235), Radius: 0.024},
		},
		Interactables: []Interactable{
			{Hint: "lamp", Handler: "state", StateAction: "toggle(is_on)"},
		},
	}
	local.Interactables[0].index = 0
	local.SetSphereInteract(2, 0)

	span := ReactiveSpan{
		Spheres:       [2]int{0, 3},
		Lights:        [2]int{0, 1},
		Interactables: [2]int{0, 1},
	}
	SpliceFragment(dst, &span, local)

	sphereIdx, ok := dst.InteractableSphereIndex(0)
	if !ok {
		t.Fatal("sphere pick map missing after splice")
	}
	if sphereIdx != 2 {
		t.Fatalf("sphereInteract = %d, want 2 (bulb not elbow)", sphereIdx)
	}
}

func vecFrom(x, y, z float64) vec.V {
	return vec.New(x, y, z)
}
