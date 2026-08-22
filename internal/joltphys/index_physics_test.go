package joltphys

import (
	"strings"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/door"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestIndexSceneChairPhysics(t *testing.T) {
	sc, err := sceneio.Load(repoPath("scenes/office-sunset/index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	dm := door.NewManager()
	if err := dm.Instantiate(sc); err != nil {
		t.Fatal(err)
	}

	var chairGroup *scene.PhysicsGroup
	for i := range sc.PhysicsGroups {
		g := sc.PhysicsGroups[i]
		if strings.Contains(g.Name, "chair") {
			chairGroup = &sc.PhysicsGroups[i]
			break
		}
	}
	if chairGroup == nil {
		t.Fatalf("no chair physics group; have %d groups", len(sc.PhysicsGroups))
	}
	db := chairGroup.Body
	for i := db.Boxes[0]; i < db.Boxes[1]; i++ {
		if !isDynamicBox(sc, i) {
			t.Fatalf("chair box %d not in dynamic bodies (duplicate static collider?)", i)
		}
	}

	cfg := camera.DefaultConfig()
	cfg.JoltPhysics = true
	w, err := NewWorldFromScene(sc, vec.New(10, 201.8, 8), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()

	if w.BodyCount() < 2 {
		t.Fatalf("body count=%d, expected static floor + dynamic chair", w.BodyCount())
	}
}
