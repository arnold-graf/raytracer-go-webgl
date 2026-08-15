package scenestate

import (
	"strings"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/sceneparam"
	"raytracer/internal/vec"
)

func TestStateUpdateMinimalChanges(t *testing.T) {
	path := "../../scenes/preview/state-lamp.toml"
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Reactive == nil || len(sc.Reactive.Fragments) == 0 {
		t.Fatal("expected reactive fragment from state-lamp include")
	}
	if len(sc.Lights) < 2 {
		t.Fatalf("lights = %d, want at least 2 (fill + lamp)", len(sc.Lights))
	}

	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}

	lampIdx := -1
	for i, l := range sc.Lights {
		if mgr.IsStateLight(i) {
			lampIdx = i
			if !l.Interactive {
				t.Fatalf("light[%d] should be interactive", i)
			}
		}
	}
	if lampIdx < 0 {
		t.Fatal("expected one state-driven lamp light")
	}

	staticIdx := -1
	for i := range sc.Lights {
		if i != lampIdx {
			staticIdx = i
			break
		}
	}
	if staticIdx < 0 {
		t.Fatal("expected static fill light")
	}

	onColor := sc.Lights[lampIdx].Color
	staticColor := sc.Lights[staticIdx].Color
	gen0 := sc.Generation()
	xform0 := sc.TransformGeneration()

	mgr.ResetEvalCount()
	var ia *scene.Interactable
	for i := range sc.Interactables {
		if sc.Interactables[i].Handler == "state" {
			ia = &sc.Interactables[i]
			break
		}
	}
	if ia == nil {
		t.Fatal("expected state interactable")
	}

	if err := mgr.HandleInteract(sc, ia); err != nil {
		t.Fatal(err)
	}

	if sc.Generation() != gen0 {
		t.Fatalf("Generation changed: %d -> %d (want no full rebuild)", gen0, sc.Generation())
	}
	if sc.TransformGeneration() <= xform0 {
		t.Fatal("TransformGeneration should advance for light-only update")
	}
	if mgr.RefreshCount() != 1 {
		t.Fatalf("refresh count = %d, want 1", mgr.RefreshCount())
	}
	if sc.Lights[staticIdx].Color != staticColor {
		t.Fatalf("static light color changed: %v -> %v", staticColor, sc.Lights[staticIdx].Color)
	}
	if sc.Lights[lampIdx].Color == onColor {
		t.Fatal("lamp color unchanged after toggle off")
	}
	if sc.Lights[lampIdx].Color.LenSq() > 1e-6 {
		t.Fatalf("lamp should be off, color = %v", sc.Lights[lampIdx].Color)
	}

	mgr.ResetEvalCount()
	xform1 := sc.TransformGeneration()
	if err := mgr.HandleInteract(sc, ia); err != nil {
		t.Fatal(err)
	}
	if mgr.RefreshCount() != 1 {
		t.Fatalf("second toggle refresh count = %d, want 1", mgr.RefreshCount())
	}
	if sc.Lights[lampIdx].Color != onColor {
		t.Fatalf("lamp color = %v, want restored %v", sc.Lights[lampIdx].Color, onColor)
	}
	if sc.TransformGeneration() <= xform1 {
		t.Fatal("TransformGeneration should advance on second toggle")
	}
}

func TestStateStoreSubscriberGraph(t *testing.T) {
	spec := &scene.ReactiveSpec{
		Fragments: []scene.ReactiveFragment{{
			ScopeID: "lamp.toml",
			State: map[string]sceneparam.StateValue{
				"lamp_on": {Kind: "bool", Boolean: true},
			},
		}},
	}
	mgr, err := NewManager(spec)
	if err != nil {
		t.Fatal(err)
	}
	key := "lamp.toml.lamp_on"
	frags := mgr.keyFragments[key]
	if len(frags) != 1 || frags[0] != 0 {
		t.Fatalf("fragments[%q] = %v, want [0]", key, frags)
	}
}

func TestStructuralIfReactivity(t *testing.T) {
	path := "../../scenes/objects/state-light-grid.toml"
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Reactive == nil || len(sc.Reactive.Fragments) == 0 {
		t.Fatal("expected reactive fragment")
	}
	lightsOn := len(sc.Lights)
	if lightsOn == 0 {
		t.Fatal("expected lights when on=true")
	}

	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	gen0 := sc.Generation()
	if err := mgr.store.Toggle("state-light-grid.toml.on"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.applyForKeys(sc, []string{"state-light-grid.toml.on"}); err != nil {
		t.Fatal(err)
	}
	if len(sc.Lights) != 0 {
		t.Fatalf("lights = %d, want 0 after structural off", len(sc.Lights))
	}
	if !mgr.StructChanged() {
		t.Fatal("expected structural change when toggling grid power")
	}
	if sc.Generation() == gen0 {
		t.Fatal("structural update should bump Generation")
	}
}

func TestBoxMaterialReactivity(t *testing.T) {
	path := "../../scenes/objects/state-panel.toml"
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	if sc.Boxes[0].Surface.Mat != scene.MatDiffuse {
		t.Fatalf("initial mat = %d, want diffuse", sc.Boxes[0].Surface.Mat)
	}
	var ia *scene.Interactable
	for i := range sc.Interactables {
		if sc.Interactables[i].Handler == "state" {
			ia = &sc.Interactables[i]
			break
		}
	}
	if ia == nil {
		t.Fatal("expected switch interactable")
	}
	gen0 := sc.Generation()
	if err := mgr.HandleInteract(sc, ia); err != nil {
		t.Fatal(err)
	}
	if sc.Boxes[0].Surface.Mat != scene.MatEmit {
		t.Fatalf("mat = %d, want emit after toggle", sc.Boxes[0].Surface.Mat)
	}
	if sc.Generation() == gen0 {
		t.Fatal("material change should bump Generation")
	}
	if mgr.StructChanged() {
		t.Fatal("panel toggle should not change primitive counts")
	}
}

func TestReactiveOnUseRefresh(t *testing.T) {
	path := "../../scenes/objects/state-panel.toml"
	sc, err := sceneio.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	var ia *scene.Interactable
	for i := range sc.Interactables {
		if sc.Interactables[i].Handler == "state" {
			ia = &sc.Interactables[i]
			break
		}
	}
	if ia == nil || ia.StateAction != "toggle(lit)" {
		t.Fatalf("interactable = %+v", ia)
	}
	if err := mgr.HandleInteract(sc, ia); err != nil {
		t.Fatal(err)
	}
	if _, ok := mgr.actions[ia.Index()]; !ok {
		t.Fatal("action map should still contain switch after refresh")
	}
}

func TestDeskLampInstancedToggleUpdatesTemplate(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/server-room-1.toml")
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	var tmpl *scene.Scene
	for _, t := range sc.Instancing().Templates {
		if strings.Contains(t.Source, "desk-anglepoise-lamp") {
			tmpl = t.Scene
			break
		}
	}
	if tmpl == nil {
		t.Fatal("desk lamp template not found")
	}
	if mat, ok := deskLampBulbMat(tmpl); !ok || mat != scene.MatGlass {
		t.Fatalf("initial template bulb mat = %v, ok=%v", mat, ok)
	}
	if len(tmpl.Lights) != 1 {
		t.Fatalf("template lights = %d, want 1 when on", len(tmpl.Lights))
	}
	lampKey := findDeskLampStateKey(sc.Reactive, "@p")
	if lampKey == "" {
		t.Fatalf("instanced lamp state keys = %v", deskLampStateKeys(sc.Reactive))
	}
	if err := mgr.store.Toggle(lampKey); err != nil {
		t.Fatal(err)
	}
	if err := mgr.applyForKeys(sc, []string{lampKey}); err != nil {
		t.Fatal(err)
	}
	tmpl = nil
	for _, t := range sc.Instancing().Templates {
		if strings.Contains(t.Source, "desk-anglepoise-lamp") {
			tmpl = t.Scene
			break
		}
	}
	if mat, ok := deskLampBulbMat(tmpl); !ok || mat != scene.MatGlass {
		t.Fatalf("template bulb mat after off = %v, ok=%v", mat, ok)
	}
	if len(tmpl.Lights) != 0 {
		t.Fatalf("template lights = %d, want 0 when off", len(tmpl.Lights))
	}
}

func deskLampBulbMat(s *scene.Scene) (int, bool) {
	for _, sp := range s.Spheres {
		if sp.Center.Y > 0.36 && sp.Center.Y < 0.38 && sp.Radius < 0.03 {
			return sp.Surface.Mat, true
		}
	}
	return 0, false
}

func TestFrontOfficeSurvivesLampToggle(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	before := countFOBoxes(sc)
	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	keys := deskLampStateKeys(sc.Reactive)
	if len(keys) == 0 {
		t.Fatal("expected desk lamp state keys")
	}
	// Toggle only the front-office desk lamp; server-room lamp must stay unchanged.
	foKey := findDeskLampStateKey(sc.Reactive, "front-office-desk")
	if foKey == "" {
		t.Fatalf("front office lamp key not found in %v", keys)
	}
	srKey := findDeskLampStateKey(sc.Reactive, "@p")
	if srKey != "" {
		srBefore, _ := mgr.store.Lookup(srKey)
		if err := mgr.store.Toggle(foKey); err != nil {
			t.Fatal(err)
		}
		if err := mgr.applyForKeys(sc, []string{foKey}); err != nil {
			t.Fatal(err)
		}
		srAfter, _ := mgr.store.Lookup(srKey)
		if srBefore.Boolean != srAfter.Boolean {
			t.Fatalf("server room lamp state changed when toggling front office lamp")
		}
	} else {
		if err := mgr.store.Toggle(foKey); err != nil {
			t.Fatal(err)
		}
		if err := mgr.applyForKeys(sc, []string{foKey}); err != nil {
			t.Fatal(err)
		}
	}
	after := countFOBoxes(sc)
	t.Logf("FO world boxes before toggle=%d after=%d", before, after)
	if after < before/2 {
		t.Fatalf("lamp toggle wiped front office: before=%d after=%d", before, after)
	}
}

func countFOBoxes(sc *scene.Scene) int {
	n := 0
	for _, b := range sc.Boxes {
		cx := (b.Min.X + b.Max.X) / 2
		cy := (b.Min.Y + b.Max.Y) / 2
		cz := (b.Min.Z + b.Max.Z) / 2
		if b.Xform != nil {
			c := b.Xform.ToWorld(vec.V{X: cx, Y: cy, Z: cz})
			cx, cy = c.X, c.Y
		}
		if cx >= 38 && cx <= 52 && cy >= 199 && cy <= 206 {
			n++
		}
	}
	return n
}
