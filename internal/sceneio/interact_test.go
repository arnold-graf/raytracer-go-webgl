package sceneio

import (
	"testing"
)

func TestInteractDecodes(t *testing.T) {
	data := []byte(`
[interact]
hint = "press E to test"
use_range = 2.0
on_use = "test_handler"
center = [1.0, 2.0, 3.0]
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
}
