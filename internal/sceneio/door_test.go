package sceneio_test

import (
	"os"
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestLoadVillaDoor(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var found bool
	for _, ds := range sc.DoorSpecs {
		if ds.ID == "villa_front_door" {
			found = true
			if len(ds.PanelBoxes) != 1 {
				t.Fatalf("panel_boxes = %v, want 1", ds.PanelBoxes)
			}
		}
	}
	if !found {
		t.Fatal("villa_front_door not found in merged scene")
	}
	var interact bool
	for _, ia := range sc.Interactables {
		if ia.Handler == "door" && ia.DoorID == "villa_front_door" {
			interact = true
		}
	}
	if !interact {
		t.Fatal("door interactable missing")
	}
}

func TestLoadDoorTOML(t *testing.T) {
	const body = `
[[box]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
width = 1.0
height = 2.0
depth = 0.08
material = "diffuse"
albedo = [0.5, 0.5, 0.5]

[[door]]
id = "d1"
panel_boxes = [0]
hinge = [0.0, 0.0, 0.0]

  [door.interact]
  center = [0.5, 1.0, 0.04]
  use_range = 2.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "door-test.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(sc.DoorSpecs) != 1 || sc.DoorSpecs[0].ID != "d1" {
		t.Fatalf("DoorSpecs = %+v", sc.DoorSpecs)
	}
}
