package scenestate

import (
	"strings"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
)

func TestFrontOfficeLightSwitchToggleViaIndex(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	var iaIdx = -1
	for i, ia := range sc.Interactables {
		if ia.Hint == "light switch" {
			iaIdx = i
			break
		}
	}
	if iaIdx < 0 {
		t.Fatal("light switch interactable not found")
	}
	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	action, ok := mgr.actions[iaIdx]
	if !ok {
		t.Fatal("no action registered for light switch")
	}
	if action.scopedKey() != "index.toml#1/server-room-front-office.toml.is_ceiling_light_on" {
		t.Fatalf("action key = %q", action.scopedKey())
	}
	lightsBefore := countLights(sc)
	if err := mgr.HandleInteract(sc, &sc.Interactables[iaIdx]); err != nil {
		t.Fatal(err)
	}
	if len(sc.Lights) >= lightsBefore {
		t.Fatalf("lights = %d after off, want fewer than %d", len(sc.Lights), lightsBefore)
	}
	v, ok := mgr.store.Lookup("index.toml#1/server-room-front-office.toml.is_ceiling_light_on")
	if !ok || v.Boolean {
		t.Fatalf("ceiling state = %v, ok=%v", v, ok)
	}
}

func TestFrontOfficeLightSwitchToggle(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/server-room-front-office.toml")
	if err != nil {
		t.Fatal(err)
	}
	var iaIdx = -1
	for i, ia := range sc.Interactables {
		if ia.Hint == "light switch" {
			iaIdx = i
			if ia.Handler != "state" {
				t.Fatalf("handler = %q, want state", ia.Handler)
			}
			if ia.StateAction != "toggle(is_ceiling_light_on)" {
				t.Fatalf("StateAction = %q", ia.StateAction)
			}
			break
		}
	}
	if iaIdx < 0 {
		t.Fatal("light switch interactable not found")
	}
	lightsBefore := countLights(sc)
	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	if err := mgr.HandleInteract(sc, &sc.Interactables[iaIdx]); err != nil {
		t.Fatal(err)
	}
	lightsAfterOff := len(sc.Lights)
	if lightsAfterOff >= lightsBefore {
		t.Fatalf("lights = %d after off, want fewer than %d", lightsAfterOff, lightsBefore)
	}
	v, ok := mgr.store.Lookup("server-room-front-office.toml.is_ceiling_light_on")
	if !ok {
		t.Fatal("state key missing")
	}
	if v.Boolean {
		t.Fatal("expected is_ceiling_light_on false after toggle")
	}
	boxes := len(sc.Boxes)
	if err := mgr.HandleInteract(sc, &sc.Interactables[iaIdx]); err != nil {
		t.Fatal(err)
	}
	if len(sc.Lights) <= lightsAfterOff {
		t.Fatalf("lights = %d after on, want more than %d", len(sc.Lights), lightsAfterOff)
	}
	if len(sc.Boxes) != boxes {
		t.Fatalf("boxes = %d after refresh, want %d", len(sc.Boxes), boxes)
	}
	if _, ok := mgr.actions[iaIdx]; !ok {
		t.Fatal("action map should still contain switch after refresh")
	}
}

func TestFrontOfficeDeskLampToggle(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/server-room-front-office.toml")
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
	var iaIdx = -1
	for i, ia := range sc.Interactables {
		if ia.Hint != "lamp" {
			continue
		}
		if ia.Index() != i {
			t.Fatalf("lamp slice[%d].Index() = %d", i, ia.Index())
		}
		iaIdx = i
		break
	}
	if iaIdx < 0 {
		t.Fatal("lamp interactable not found")
	}
	sphereBefore, ok := sc.InteractableSphereIndex(iaIdx)
	if !ok {
		t.Fatal("lamp sphere pick not registered")
	}
	if y := sc.Spheres[sphereBefore].Center.Y; y < 0.35 || y > 0.39 {
		t.Fatalf("lamp pick sphere y = %v, want bulb ~0.37", y)
	}
	ceiling, _ := mgr.store.Lookup("server-room-front-office.toml.is_ceiling_light_on")
	lampKey := findDeskLampStateKey(sc.Reactive, "front-office-desk")
	if lampKey == "" {
		t.Fatalf("desk lamp state keys = %v", deskLampStateKeys(sc.Reactive))
	}
	lamp, _ := mgr.store.Lookup(lampKey)
	_ = lamp
	lightsBefore := len(sc.Lights)
	if err := mgr.HandleInteract(sc, &sc.Interactables[iaIdx]); err != nil {
		t.Fatal(err)
	}
	ceilingAfter, _ := mgr.store.Lookup("server-room-front-office.toml.is_ceiling_light_on")
	lampAfter, _ := mgr.store.Lookup(lampKey)
	if ceilingAfter.Boolean != ceiling.Boolean {
		t.Fatalf("ceiling light state changed unexpectedly")
	}
	if lampAfter.Boolean {
		t.Fatal("expected lamp is_on false")
	}
	if len(sc.Lights) != lightsBefore-1 {
		t.Fatalf("lights = %d, want %d", len(sc.Lights), lightsBefore-1)
	}
	if mgr.RefreshCount() != 1 {
		t.Fatalf("refresh count = %d, want 1", mgr.RefreshCount())
	}
	sphereAfter, ok := sc.InteractableSphereIndex(iaIdx)
	if !ok {
		t.Fatal("lamp sphere pick not registered after toggle off")
	}
	if y := sc.Spheres[sphereAfter].Center.Y; y < 0.35 || y > 0.39 {
		t.Fatalf("lamp pick sphere y = %v after toggle off, want bulb ~0.37", y)
	}
	if sc.Spheres[sphereAfter].Radius < 0.02 {
		t.Fatalf("lamp pick sphere radius = %v after toggle off, want bulb ~0.024", sc.Spheres[sphereAfter].Radius)
	}
}

func TestIndependentDeskLampStateInIndex(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
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
	keys := deskLampStateKeys(sc.Reactive)
	if len(keys) < 2 {
		t.Fatalf("expected at least 2 desk lamp state keys, got %v", keys)
	}
	foKey := findDeskLampStateKey(sc.Reactive, "front-office-desk")
	srKey := findDeskLampStateKey(sc.Reactive, "@p")
	if foKey == "" || srKey == "" {
		t.Fatalf("foKey=%q srKey=%q all=%v", foKey, srKey, keys)
	}
	if foKey == srKey {
		t.Fatalf("expected distinct lamp scopes, got %q", foKey)
	}
	if err := mgr.store.Toggle(foKey); err != nil {
		t.Fatal(err)
	}
	if err := mgr.applyForKeys(sc, []string{foKey}); err != nil {
		t.Fatal(err)
	}
	foAfter, _ := mgr.store.Lookup(foKey)
	srAfter, _ := mgr.store.Lookup(srKey)
	if foAfter.Boolean {
		t.Fatal("front office lamp should be off after toggle")
	}
	if !srAfter.Boolean {
		t.Fatal("server room lamp should stay on when front office lamp toggles")
	}
}

func TestToggleFrontOfficeLampDoesNotRewriteInstancedTemplate(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/office-sunset/index.toml")
	if err != nil {
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
		t.Fatal("instanced desk lamp template not found")
	}
	lightsBefore := len(tmpl.Lights)
	mgr, err := NewManager(sc.Reactive)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Instantiate(sc); err != nil {
		t.Fatal(err)
	}
	foKey := findDeskLampStateKey(sc.Reactive, "front-office-desk")
	if foKey == "" {
		t.Fatal("front office lamp state key not found")
	}
	if err := mgr.store.Toggle(foKey); err != nil {
		t.Fatal(err)
	}
	if err := mgr.applyForKeys(sc, []string{foKey}); err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range sc.Instancing().Templates {
		if !strings.Contains(tmpl.Source, "desk-anglepoise-lamp") {
			continue
		}
		if len(tmpl.Scene.Lights) != lightsBefore {
			t.Fatalf("instanced template lights = %d, want %d (front office toggle must not rewrite BLAS)",
				len(tmpl.Scene.Lights), lightsBefore)
		}
	}
}

func countLights(sc *scene.Scene) int {
	if sc == nil {
		return 0
	}
	return len(sc.Lights)
}

func deskLampStateKeys(spec *scene.ReactiveSpec) []string {
	if spec == nil {
		return nil
	}
	var keys []string
	for _, frag := range spec.Fragments {
		if _, ok := frag.State["is_on"]; !ok {
			continue
		}
		keys = append(keys, scopedKey(frag.ScopeID, "is_on"))
	}
	return keys
}

func findDeskLampStateKey(spec *scene.ReactiveSpec, scopeContains string) string {
	for _, key := range deskLampStateKeys(spec) {
		if scopeContains == "" || strings.Contains(key, scopeContains) {
			return key
		}
	}
	return ""
}
