package sceneio_test

import (
	"math"
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

func TestDoorPanelFileProps(t *testing.T) {
	panelBody := `
[props]
width = 4.0
height = 4.0
depth = 0.1

[[box]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
width = 'width'
height = 'height'
depth = 'depth'
material = "diffuse"
albedo = [0.5, 0.5, 0.5]
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panel.toml"), []byte(panelBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without panel_file_props, parent [props] cascade into the panel load.
	noProps := filepath.Join(dir, "no_props.toml")
	if err := os.WriteFile(noProps, []byte(`
[props]
width = 100.0
height = 25.0
depth = 100.0

[[door]]
id = "d1"
panel_file = "panel.toml"
hinge = [0.0, 0.0, 0.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := sceneio.Load(noProps)
	if err != nil {
		t.Fatalf("load without panel_file_props: %v", err)
	}
	if len(sc.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(sc.Boxes))
	}
	b := sc.Boxes[0]
	if math.Abs(b.Max.X-b.Min.X-100.0) > 1e-9 {
		t.Fatalf("width = %v, want 100 from parent props", b.Max.X-b.Min.X)
	}

	// Explicit panel_file_props override parent inheritance.
	withProps := filepath.Join(dir, "with_props.toml")
	if err := os.WriteFile(withProps, []byte(`
[props]
width = 100.0
height = 25.0
depth = 100.0

[[door]]
id = "d1"
panel_file = "panel.toml"
panel_file_props = { width = 'width', height = 10.0, depth = 1.0 }
hinge = [0.0, 0.0, 0.0]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sc2, err := sceneio.Load(withProps)
	if err != nil {
		t.Fatalf("load with panel_file_props: %v", err)
	}
	if len(sc2.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(sc2.Boxes))
	}
	b2 := sc2.Boxes[0]
	if math.Abs(b2.Max.X-b2.Min.X-100.0) > 1e-9 {
		t.Fatalf("width = %v, want 100 from parent expr", b2.Max.X-b2.Min.X)
	}
	if math.Abs(b2.Max.Y-b2.Min.Y-10.0) > 1e-9 {
		t.Fatalf("height = %v, want 10 from explicit prop", b2.Max.Y-b2.Min.Y)
	}
	if math.Abs(b2.Max.Z-b2.Min.Z-1.0) > 1e-9 {
		t.Fatalf("depth = %v, want 1 from explicit prop", b2.Max.Z-b2.Min.Z)
	}
}
