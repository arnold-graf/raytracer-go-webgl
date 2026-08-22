package joltphys

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/camera"
	"raytracer/internal/door"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestOfficeSunsetJoltWorld(t *testing.T) {
	sc, err := sceneio.Load(repoPath("scenes/office-sunset/index.toml"))
	if err != nil {
		t.Fatal(err)
	}
	dm := door.NewManager()
	if err := dm.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	doorBodies := 0
	for _, db := range sc.DynamicBodies {
		if strings.HasPrefix(db.Name, "door_") {
			doorBodies++
		}
	}
	if doorBodies == 0 {
		t.Fatal("expected door dynamic bodies")
	}

	cfg := camera.DefaultConfig()
	cfg.JoltPhysics = true
	w, err := NewWorldFromScene(sc, vec.New(10, 201.8, 8), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()
	if w.BodyCount() < doorBodies {
		t.Fatalf("bodies=%d, want at least %d door kinematic bodies", w.BodyCount(), doorBodies)
	}
}

// Door panels are thin (~0.06 m); Jolt's default convex radius is 0.05 m.
func TestCreateBoxDoorPanelHalfExtents(t *testing.T) {
	shape := jolt.CreateBox(jolt.Vec3{X: 0.5, Y: 1.5, Z: 0.03})
	if shape == nil {
		t.Fatal("CreateBox returned nil for door panel half extents")
	}
	defer shape.Destroy()
}

func repoPath(rel string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, rel)
}
