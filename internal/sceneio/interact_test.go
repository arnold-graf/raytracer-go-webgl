package sceneio

import (
	"testing"

	"raytracer/internal/vec"
)

func TestBoxInteractDecodes(t *testing.T) {
	data := []byte(`
[[box]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
width = 1.0
height = 1.0
depth = 1.0
material = "diffuse"
hint = "press E to test"
use_range = 2.0
on_use = "test_handler"
`)
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Interactables) != 1 {
		t.Fatalf("got %d interactables", len(got.Interactables))
	}
	ia := got.Interactables[0]
	if ia.Hint != "press E to test" || ia.Handler != "test_handler" || ia.Range != 2 {
		t.Fatalf("unexpected interactable: %+v", ia)
	}
	ray := vec.Ray{Origin: vec.New(0.5, 0.5, -2), Dir: vec.New(0, 0, 1)}
	picked := got.PickInteractable(ray)
	if picked == nil || picked.Handler != "test_handler" {
		t.Fatalf("PickInteractable = %v", picked)
	}
}
