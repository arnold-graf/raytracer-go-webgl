package door

import (
	"math"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestSnapshotRestorePreservesOpenDoor(t *testing.T) {
	sc1, mgr1 := testDoorScene(t)
	mgr1.Toggle(sc1, "test", vec.New(1.5, 1, -1))
	for i := 0; i < 120; i++ {
		mgr1.Update(sc1, vec.New(5, 1.6, 5), 0, 1.75, 1.0/60.0)
	}
	if mgr1.agents[0].State != stateOpen {
		t.Fatalf("state = %q, want open", mgr1.agents[0].State)
	}
	saved := mgr1.Snapshot(sc1)

	sc2, _ := testDoorScene(t)
	mgr2 := NewManager()
	if err := mgr2.Instantiate(sc2); err != nil {
		t.Fatal(err)
	}
	if mgr2.agents[0].State != stateClosed {
		t.Fatal("fresh instantiate should start closed")
	}
	mgr2.Restore(sc2, saved)

	a := &mgr2.agents[0]
	if a.State != stateOpen {
		t.Fatalf("restored state = %q, want open", a.State)
	}
	if math.Abs(a.Panels[0].Angle-a.Panels[0].Target) > 1e-4 {
		t.Fatalf("restored angle = %v, target = %v", a.Panels[0].Angle, a.Panels[0].Target)
	}
}

func TestSnapshotRestorePreservesDuplicateIDs(t *testing.T) {
	spec := func(hinge vec.V, boxes [2]int) scene.DoorSpec {
		return scene.DoorSpec{
			ID: "d", Kind: "single", Hinge: hinge, OpenAngle: math.Pi / 2, Speed: 10,
			Panels: []scene.DoorPanelGeom{{Boxes: boxes}},
			Interact: &scene.Interactable{DoorID: "d"},
		}
	}
	makeScene := func() *scene.Scene {
		return &scene.Scene{
			DoorSpecs: []scene.DoorSpec{
				spec(vec.New(0, 0, 0), [2]int{0, 1}),
				spec(vec.New(10, 0, 0), [2]int{1, 2}),
			},
			Boxes: []scene.Box{
				{Min: vec.New(0, 0, 0), Max: vec.New(0.08, 2, 1)},
				{Min: vec.New(10, 0, 0), Max: vec.New(10.08, 2, 1)},
			},
		}
	}
	sc1 := makeScene()
	mgr1 := NewManager()
	if err := mgr1.Instantiate(sc1); err != nil {
		t.Fatal(err)
	}
	ia := &scene.Interactable{DoorID: "d", BoxIndex: 1}
	mgr1.ToggleInteract(ia, vec.New(10, 1.6, 1))
	for i := 0; i < 60; i++ {
		mgr1.Update(sc1, vec.New(10, 1.6, 1), 0, 1.75, 1.0/60.0)
	}
	saved := mgr1.Snapshot(sc1)

	sc2 := makeScene()
	mgr2 := NewManager()
	if err := mgr2.Instantiate(sc2); err != nil {
		t.Fatal(err)
	}
	mgr2.Restore(sc2, saved)

	if mgr2.agents[0].State != stateClosed {
		t.Fatalf("first door state = %q, want closed", mgr2.agents[0].State)
	}
	if mgr2.agents[1].State == stateClosed {
		t.Fatalf("second door should stay open, state = %q", mgr2.agents[1].State)
	}
}
