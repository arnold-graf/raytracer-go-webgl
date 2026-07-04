package sceneio_test

import (
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestLoadCupboardDoubleDoor(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "objects", "cupboard-double-door.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.DoorSpecs) != 1 || sc.DoorSpecs[0].Kind != "double" {
		t.Fatalf("DoorSpecs = %+v", sc.DoorSpecs)
	}
	if len(sc.DoorSpecs[0].Panels) != 2 {
		t.Fatalf("panels = %v", sc.DoorSpecs[0].Panels)
	}
	if len(sc.Boxes) < 6 {
		t.Fatalf("boxes = %d, want carcass + 2 door panels + shelves", len(sc.Boxes))
	}
}

func TestLoadServerRoomCupboard(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "office-sunset", "server-room-1.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range sc.DoorSpecs {
		if d.ID == "cupboard_doors" {
			found = true
			if d.Kind != "double" {
				t.Fatalf("kind = %q", d.Kind)
			}
		}
	}
	if !found {
		t.Fatal("cupboard_doors not found")
	}
}
