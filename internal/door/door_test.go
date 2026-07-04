package door

import (
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

func testDoorScene(t *testing.T) (*scene.Scene, *Manager) {
	boxes := []scene.Box{
		{Min: vec.New(0, 0, 0), Max: vec.New(1, 3, 0.2)},
		{Min: vec.New(1, 0, 0), Max: vec.New(2, 2.5, 0.08)},
	}
	sc := &scene.Scene{
		Boxes: boxes,
		DoorSpecs: []scene.DoorSpec{{
			ID:         "test",
			Kind:       "single",
			Hinge:      vec.New(1, 0, 0),
			Axis:       "y",
			OpenAngle:  math.Pi / 2,
			Swing:      "one_way",
			OpenSign:   1,
			Speed:      3,
			PanelBoxes: []int{1},
		}},
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	sc.SetDoorGhost(mgr.GhostBox)
	return sc, mgr
}

func TestClosedDoorBlocksPlayer(t *testing.T) {
	sc, _ := testDoorScene(t)
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45
	if !sc.Blocked(1.5, 0.04, feetY, headY, r, step) {
		t.Fatal("closed door panel should block player in doorway")
	}
}

func TestGhostDuringSwing(t *testing.T) {
	sc, mgr := testDoorScene(t)
	mgr.Toggle(sc, "test", vec.New(1.5, 1, -1))
	for i := 0; i < 5; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.04), 0, 1.75, 1.0/30.0)
	}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45
	if sc.Blocked(1.5, 0.04, feetY, headY, r, step) {
		t.Fatal("animating door should ghost through player")
	}
}

func TestCollisionRestoresAfterPlayerLeaves(t *testing.T) {
	sc, mgr := testDoorScene(t)
	// Open through the player (ghost), finish open, then close after player leaves.
	mgr.Toggle(sc, "test", vec.New(1.5, 1, -1))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.04), 0, 1.75, 1.0/60.0)
	}
	for i := 0; i < 30; i++ {
		mgr.Update(sc, vec.New(5, 1.6, 5), 0, 1.75, 1.0/60.0)
	}
	mgr.Toggle(sc, "test", vec.New(5, 1.6, 5))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(5, 1.6, 5), 0, 1.75, 1.0/60.0)
	}
	feetY, headY, r, step := 0.0, 2.0, 0.3, 0.45
	if !sc.Blocked(1.5, 0.04, feetY, headY, r, step) {
		t.Fatal("closed door panel should block again after player clears overlap")
	}
}

func TestStaticObstacleClampsSwing(t *testing.T) {
	sc, mgr := testDoorScene(t)
	a := &mgr.agents[0]
	p := &a.Panels[0]
	skip := mgr.skipBoxFunc()
	// Furniture in the swing path (not touching the closed panel).
	sc.Boxes = append(sc.Boxes, scene.Box{
		Min: vec.New(1.05, 0, 0.12), Max: vec.New(1.95, 2.0, 0.45),
	})
	proposed := clampAngle(sc, a, p, math.Pi/4, skip)
	if proposed >= math.Pi/2-0.05 {
		t.Fatalf("clamp should stop before full open, got %v", proposed)
	}
	if proposed < 0.05 {
		t.Fatalf("clamp should allow partial open, got %v", proposed)
	}
}

func TestDoubleDoorOpensBothPanels(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(0, 0, 0), Max: vec.New(0.08, 2.5, 1)},
			{Min: vec.New(2.92, 0, 0), Max: vec.New(3, 2.5, 1)},
		},
		DoorSpecs: []scene.DoorSpec{{
			ID: "double", Kind: "double",
			Hinge: vec.New(0, 0, 0), HingeRight: vec.New(3, 0, 0),
			Axis: "y", OpenAngle: math.Pi / 2, Swing: "one_way", OpenSign: 1, Speed: 10,
			PanelBoxes: []int{0, 1},
		}},
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	mgr.Toggle(sc, "double", vec.New(1.5, 1, -1))
	for i := 0; i < 90; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, -2), 0, 1.75, 1.0/60.0)
	}
	left := mgr.agents[0].Panels[0].Angle
	right := mgr.agents[0].Panels[1].Angle
	if left >= -0.1 || right <= 0.1 {
		t.Fatalf("double doors should split open, left=%v right=%v", left, right)
	}
}

func TestCupboardDoorsSwingOutward(t *testing.T) {
	// Cupboard doors sit in -Z from the front face (pos_z = -thickness); they must
	// swing toward -Z (into the room), not +Z into the carcass.
	sc := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(0.06, 0, -0.06), Max: vec.New(1.0, 3, 0)},
			{Min: vec.New(1.0, 0, -0.06), Max: vec.New(1.94, 3, 0)},
		},
		DoorSpecs: []scene.DoorSpec{{
			ID: "cupboard", Kind: "double",
			Hinge: vec.New(0.06, 0, 0), HingeRight: vec.New(1.94, 0, 0),
			Axis: "y", OpenAngle: math.Pi / 2, Swing: "one_way", OpenSign: -1, Speed: 10,
			PanelBoxes: []int{0, 1},
		}},
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	// Free outer corner of the left leaf (max X, min Z in closed pose).
	outerCorner := func(idx int) vec.V {
		b := sc.Boxes[idx]
		return b.Xform.ToWorld(vec.V{X: b.Max.X, Y: 0, Z: b.Min.Z})
	}
	zClosed := outerCorner(0).Z
	mgr.Toggle(sc, "cupboard", vec.New(1.0, 1.5, -2))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(1.0, 1.5, -2), 0, 1.75, 1.0/60.0)
	}
	zOpen := outerCorner(0).Z
	if zOpen >= zClosed-0.05 {
		t.Fatalf("door should swing toward -Z (outward); closed z=%.3f open z=%.3f", zClosed, zOpen)
	}
}

func TestCupboardDoorsOpenFully(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "objects", "cupboard-double-door.toml")
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	mgr.Toggle(sc, "cupboard_doors", vec.New(0.8, 1.5, -2))
	for i := 0; i < 300; i++ {
		mgr.Update(sc, vec.New(0.8, 1.5, -2), 0, 1.75, 1.0/60.0)
	}
	left := math.Abs(mgr.agents[0].Panels[0].Angle) * 180 / math.Pi
	right := math.Abs(mgr.agents[0].Panels[1].Angle) * 180 / math.Pi
	t.Logf("final angles: left=%.1f° right=%.1f° state=%s", left, right, mgr.agents[0].State)
	if left < 80 || right < 80 {
		t.Fatalf("each panel should open ~90°, got left=%.1f° right=%.1f°", left, right)
	}
}

func TestBothWaySwingPicksPlayerSide(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(1, 0, 0), Max: vec.New(2, 2.5, 0.08)},
		},
		DoorSpecs: []scene.DoorSpec{{
			ID: "both", Kind: "single",
			Hinge: vec.New(1, 0, 0), Axis: "y",
			OpenAngle: math.Pi / 2, Swing: "both", Speed: 10,
			PanelBoxes: []int{0},
		}},
	}
	mgr := NewManager()
	_ = mgr.Instantiate(sc)
	mgr.Toggle(sc, "both", vec.New(1.5, 1, -1)) // player on -Z side
	for i := 0; i < 60; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, -1), 0, 1.75, 1.0/60.0)
	}
	angle := mgr.agents[0].Panels[0].Angle
	if math.Abs(angle) < 0.1 {
		t.Fatalf("both-way door should swing open, angle=%v", angle)
	}
}
