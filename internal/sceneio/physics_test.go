package sceneio

import (
	"os"
	"testing"

	"raytracer/internal/scene"
)

func TestPhysicsIncludeCompound(t *testing.T) {
	dir := t.TempDir()
	obj := writeTemp(t, dir, "desk.toml", `
[physics]
mode = "compound"
mass = 12.5

[[box]]
material = "diffuse"
pos_x = 0
pos_y = 0.74
pos_z = 0
width = 1.0
height = 0.08
depth = 0.6
`)
	scenePath := writeTemp(t, dir, "scene.toml", `
[[include]]
file = "desk.toml"
at = [0, 0, 0]
`)
	sc, err := Load(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.PhysicsGroups) != 1 {
		t.Fatalf("physics groups = %d, want 1", len(sc.PhysicsGroups))
	}
	g := sc.PhysicsGroups[0]
	if g.Spec.Mode != scene.PhysicsCompound {
		t.Fatalf("mode = %q, want compound", g.Spec.Mode)
	}
	if g.Spec.MassKg != 12.5 {
		t.Fatalf("mass = %v, want 12.5 kg", g.Spec.MassKg)
	}
	if g.Body.Boxes[1]-g.Body.Boxes[0] != 1 {
		t.Fatalf("expected one box in dynamic body, got span %v", g.Body.Boxes)
	}
	_ = obj
}

func TestPhysicsIncludePieces(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "bits.toml", `
[[box]]
material = "diffuse"
pos_x = 0
pos_y = 0.1
pos_z = 0
width = 0.2
height = 0.2
depth = 0.2

[[sphere]]
material = "diffuse"
center = [0.3, 0.15, 0]
radius = 0.1
`)
	scenePath := writeTemp(t, dir, "scene.toml", `
[[include]]
file = "bits.toml"
at = [0, 0, 0]

[include.physics]
mode = "pieces"
mass = 2.0
`)
	sc, err := Load(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.PhysicsGroups) != 2 {
		t.Fatalf("physics groups = %d, want 2", len(sc.PhysicsGroups))
	}
}

func TestPhysicsNestedIncludeMerge(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "desk.toml", `
[[box]]
material = "diffuse"
pos_x = 0
pos_y = 0.74
pos_z = 0
width = 1.0
height = 0.08
depth = 0.6
[physics]
mode = "compound"
mass = 12.5
`)
	writeTemp(t, dir, "room.toml", `
[[include]]
file = "desk.toml"
at = [0, 0, 0]
`)
	scenePath := writeTemp(t, dir, "index.toml", `
[[include]]
file = "room.toml"
at = [5, 0, 0]
`)
	sc, err := Load(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.PhysicsGroups) != 1 {
		t.Fatalf("physics groups = %d, want 1", len(sc.PhysicsGroups))
	}
	g := sc.PhysicsGroups[0]
	if g.Body.Boxes[0] != 0 {
		t.Fatalf("box start = %d, want 0 after nested merge", g.Body.Boxes[0])
	}
	if g.Body.Boxes[1]-g.Body.Boxes[0] != 1 {
		t.Fatalf("expected one box in dynamic body, got span %v", g.Body.Boxes)
	}
}

func writeTemp(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
