package interactlight

import (
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestInteractiveLightToggleFade(t *testing.T) {
	sc := &scene.Scene{
		Lights: []scene.Light{{
			Pos:         vec.New(0, 2, 0),
			Color:       vec.New(2, 2, 2),
			Radius:      0.1,
			Interactive: true,
			Hint:        "desk lamp",
		}},
	}
	m := NewManager()
	m.Instantiate(sc, nil)
	m.ToggleInteract(&scene.Interactable{LightIndex: 0})
	m.Update(sc, 0.1)
	got := sc.Lights[0].Color
	if got.X >= 1.9 || got.X <= 0.1 {
		t.Fatalf("mid fade color = %v, want between on and off", got)
	}
	m.Update(sc, 0.5)
	got = sc.Lights[0].Color
	if got.LenSq() > 1e-6 {
		t.Fatalf("off color = %v, want ~0", got)
	}
	m.ToggleInteract(&scene.Interactable{LightIndex: 0})
	m.Update(sc, 0.5)
	got = sc.Lights[0].Color
	if got.X < 1.5 {
		t.Fatalf("on color = %v, want ~2", got)
	}
}

func TestPickInteractiveLight(t *testing.T) {
	sc := &scene.Scene{
		Lights: []scene.Light{{
			Pos:         vec.New(0, 0, 0),
			Color:       vec.New(1, 1, 1),
			Radius:      0.1,
			Interactive: true,
		}},
	}
	m := NewManager()
	m.Instantiate(sc, nil)
	ray := vec.Ray{Origin: vec.New(0, 0, -2), Dir: vec.New(0, 0, 1)}
	ia := sc.PickInteractable(ray)
	if ia == nil || ia.Handler != "light_toggle" || ia.LightIndex != 0 {
		t.Fatalf("pick = %+v", ia)
	}
	if ia.Hint != "lamp" {
		t.Fatalf("hint = %q, want lamp", ia.Hint)
	}
}
