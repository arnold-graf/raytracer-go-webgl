package door

import (
	"math"
	"os"
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
			ID:        "test",
			Kind:      "single",
			Hinge:     vec.New(1, 0, 0),
			Axis:      "y",
			OpenAngle: math.Pi / 2,
			Swing:     "one_way",
			OpenSign:  1,
			Speed:     3,
			Panels:    []scene.DoorPanelGeom{{Boxes: [2]int{1, 2}}},
			Interact: &scene.Interactable{
				Hint:    "press E",
				Handler: "door",
				DoorID:  "test",
				Range:   2.5,
			},
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
			Panels: []scene.DoorPanelGeom{
				{Boxes: [2]int{0, 1}},
				{Boxes: [2]int{1, 2}},
			},
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
			Panels: []scene.DoorPanelGeom{
				{Boxes: [2]int{0, 1}},
				{Boxes: [2]int{1, 2}},
			},
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
			Panels: []scene.DoorPanelGeom{{Boxes: [2]int{0, 1}}},
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

func testSlidingDoorScene(t *testing.T, dir vec.V) (*scene.Scene, *Manager) {
	t.Helper()
	sc := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(0, 0, 0), Max: vec.New(2, 0.2, 1)},
			{Min: vec.New(1, 0, 0), Max: vec.New(2, 2.5, 0.08)},
		},
		DoorSpecs: []scene.DoorSpec{{
			ID:           "slide",
			Kind:         "sliding",
			OpenDistance: 2.0,
			SlideDir:     dir,
			Speed:        4,
			Panels:       []scene.DoorPanelGeom{{Boxes: [2]int{1, 2}}},
		}},
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	return sc, mgr
}

func TestSlidingDoorMovesUp(t *testing.T) {
	sc, mgr := testSlidingDoorScene(t, vec.V{Y: 1})
	mgr.Toggle(sc, "slide", vec.New(1.5, 1, 0.5))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.5), 0, 1.75, 1.0/60.0)
	}
	y := sc.Boxes[1].Xform.ToWorld(vec.New(1.5, 0, 0.04)).Y
	if y < 1.9 {
		t.Fatalf("panel should slide up ~2 m, got y=%.3f", y)
	}
}

func TestSlidingDoorIgnoresStaticObstacle(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(0, 0, 0), Max: vec.New(2, 0.2, 1)},
			{Min: vec.New(1, 0, 0), Max: vec.New(2, 2.5, 0.08)},
			{Min: vec.New(1.05, 1.0, 0), Max: vec.New(1.95, 1.2, 0.5)},
		},
		DoorSpecs: []scene.DoorSpec{{
			ID:           "slide",
			Kind:         "sliding",
			OpenDistance: 2.0,
			SlideDir:     vec.V{Y: 1},
			Speed:        4,
			Panels:       []scene.DoorPanelGeom{{Boxes: [2]int{1, 2}}},
		}},
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	mgr.Toggle(sc, "slide", vec.New(1.5, 0.5, 0.5))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(1.5, 0.5, 0.5), 0, 1.75, 1.0/60.0)
	}
	if mgr.agents[0].Panels[0].Angle < 1.9 {
		t.Fatalf("sliding door should not be clamped by static geometry, offset=%.3f", mgr.agents[0].Panels[0].Angle)
	}
}

func TestSlidingDoorTOML(t *testing.T) {
	dir := t.TempDir()
	panel := filepath.Join(dir, "panel.toml")
	if err := os.WriteFile(panel, []byte(`
[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 2
depth = 0.1
material = "diffuse"
albedo = [0.5, 0.5, 0.5]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	scenePath := filepath.Join(dir, "room.toml")
	if err := os.WriteFile(scenePath, []byte(`
[[door]]
id = "lift"
kind = "sliding"
direction = "right"
open_distance = 1.5
speed = 3
panel_file = "panel.toml"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := sceneio.Load(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.DoorSpecs) != 1 || sc.DoorSpecs[0].Kind != "sliding" {
		t.Fatalf("door spec: %+v", sc.DoorSpecs)
	}
	mgr := NewManager()
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	mgr.Toggle(sc, "lift", vec.New(0.5, 1, 0))
	for i := 0; i < 90; i++ {
		mgr.Update(sc, vec.New(0.5, 1, 0), 0, 1.75, 1.0/60.0)
	}
	x := sc.Boxes[0].Xform.ToWorld(vec.New(0, 0, 0)).X
	if x < 1.4 {
		t.Fatalf("panel should slide right ~1.5 m, got x=%.3f", x)
	}
}

func TestCanCloseFalse(t *testing.T) {
	sc, mgr := testDoorScene(t)
	mgr.agents[0].CanClose = false
	ia := &scene.Interactable{DoorID: "test", BoxIndex: 1}
	if !mgr.CanUseInteract(ia, vec.New(1.5, 1, -1)) {
		t.Fatal("closed non-closable door should still be openable")
	}
	mgr.Toggle(sc, "test", vec.New(1.5, 1, -1))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.04), 0, 1.75, 1.0/60.0)
	}
	if mgr.agents[0].State != stateOpen {
		t.Fatalf("door should be open, state=%s", mgr.agents[0].State)
	}
	mgr.Toggle(sc, "test", vec.New(1.5, 1.6, 0.04))
	for i := 0; i < 30; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.04), 0, 1.75, 1.0/60.0)
	}
	if mgr.agents[0].State != stateOpen {
		t.Fatalf("can_close=false should ignore close toggle, state=%s", mgr.agents[0].State)
	}
	if mgr.CanUseInteract(ia, vec.New(1.5, 1.6, 0.04)) {
		t.Fatal("open non-closable door should not be interactable")
	}
}

func TestAutocloseTimeout(t *testing.T) {
	sc, mgr := testDoorScene(t)
	mgr.agents[0].AutocloseTimeout = 1.0
	mgr.Toggle(sc, "test", vec.New(1.5, 1, -1))
	for i := 0; i < 120; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.04), 0, 1.75, 1.0/60.0)
		if mgr.agents[0].State == stateOpen {
			break
		}
	}
	if mgr.agents[0].State != stateOpen {
		t.Fatalf("door should finish opening, state=%s", mgr.agents[0].State)
	}
	for i := 0; i < 10; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.04), 0, 1.75, 1.0/60.0)
	}
	if mgr.agents[0].State != stateOpen {
		t.Fatal("door should stay open before timeout elapses")
	}
	for i := 0; i < 90; i++ {
		mgr.Update(sc, vec.New(1.5, 1.6, 0.04), 0, 1.75, 1.0/60.0)
	}
	if mgr.agents[0].State == stateOpen {
		t.Fatal("door should autoclose after timeout")
	}
}

func TestDoorPropsTOML(t *testing.T) {
	dir := t.TempDir()
	panel := filepath.Join(dir, "panel.toml")
	if err := os.WriteFile(panel, []byte(`
[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 2
depth = 0.1
material = "diffuse"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	scenePath := filepath.Join(dir, "room.toml")
	if err := os.WriteFile(scenePath, []byte(`
[[door]]
id = "trap"
kind = "single"
hinge = [0, 0, 0]
axis = "y"
panel_file = "panel.toml"
can_close = false
autoclose_timeout = 3.0
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := sceneio.Load(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	ds := sc.DoorSpecs[0]
	if ds.CanClose {
		t.Fatal("expected can_close=false")
	}
	if ds.AutocloseTimeout != 3.0 {
		t.Fatalf("autoclose_timeout=%v, want 3", ds.AutocloseTimeout)
	}
}
