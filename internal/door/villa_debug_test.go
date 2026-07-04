package door

import (
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func TestVillaDoorTogglesOnLoad(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	if len(mgr.agents) == 0 {
		t.Fatal("no door agents")
	}
	a := &mgr.agents[0]
	p := &a.Panels[0]
	skip := mgr.skipBoxFunc()
	t.Logf("panel idx=%d hinge=%v state=%s", p.BoxIndex, p.Hinge, a.State)
	if PanelHitsStatic(sc, p.BoxIndex, skip) {
		t.Log("panel hits static at rest")
	}
	mgr.Toggle(sc, a.ID, vec.New(0, 1.6, -6))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(0, 1.6, -6), 0, 1.75, 1.0/60.0)
	}
	t.Logf("after toggle: state=%s angle=%v target=%v", a.State, p.Angle, p.Target)
	if math.Abs(p.Angle) < 0.1 {
		t.Fatalf("door did not move: state=%s angle=%v", a.State, p.Angle)
	}
}

func TestToggleInteractPicksIncludedInstance(t *testing.T) {
	sc := &scene.Scene{
		DoorSpecs: []scene.DoorSpec{
			{ID: "d", Kind: "single", Hinge: vec.New(0, 0, 0), OpenAngle: math.Pi / 2, Speed: 10, PanelBoxes: []int{0},
				Interact: &scene.Interactable{Center: vec.New(0, 1, 0), DoorID: "d"}},
			{ID: "d", Kind: "single", Hinge: vec.New(10, 0, 0), OpenAngle: math.Pi / 2, Speed: 10, PanelBoxes: []int{1},
				Interact: &scene.Interactable{Center: vec.New(10, 1, 0), DoorID: "d"}},
		},
		Boxes: []scene.Box{
			{Min: vec.New(0, 0, 0), Max: vec.New(0.08, 2, 1)},
			{Min: vec.New(10, 0, 0), Max: vec.New(10.08, 2, 1)},
		},
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	ia := &scene.Interactable{Center: vec.New(10, 1, 0), DoorID: "d"}
	mgr.ToggleInteract(ia, vec.New(10, 1.6, 1))
	if mgr.agents[0].State == stateOpening {
		t.Fatal("first door should stay closed")
	}
	if mgr.agents[1].State != stateOpening {
		t.Fatal("second door should open")
	}
}
