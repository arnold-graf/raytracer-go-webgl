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
			if len(ds.Panels) != 1 {
				t.Fatalf("panels = %v, want 1", ds.Panels)
			}
		}
	}
	if !found {
		t.Fatal("villa_front_door not found in merged scene")
	}
	var interact bool
	for _, ds := range sc.DoorSpecs {
		if ds.ID == "villa_front_door" && ds.Interact != nil && ds.Interact.Handler == "door" {
			interact = true
		}
	}
	if !interact {
		t.Fatal("door interactable missing")
	}
}

func TestLoadDoorTOML(t *testing.T) {
	panelBody := `
[[box]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
width = 1.0
height = 2.0
depth = 0.08
material = "diffuse"
albedo = [0.5, 0.5, 0.5]
`
	const body = `
[[door]]
id = "d1"
panel_file = "panel.toml"
hinge = [0.0, 0.0, 0.0]
use_range = 2.0
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panel.toml"), []byte(panelBody), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if len(sc.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1 panel box merged", len(sc.Boxes))
	}
	if sc.DoorSpecs[0].Interact == nil || sc.DoorSpecs[0].Interact.Range != 2 {
		t.Fatalf("door interact = %+v", sc.DoorSpecs[0].Interact)
	}
}
