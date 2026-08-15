package scenestate

import (
	"testing"

	"raytracer/internal/interactlight"
	"raytracer/internal/sceneio"
)

func TestCeilingTogglePreservesUplightInteract(t *testing.T) {
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
	lights := interactlight.NewManager()
	skip := func(i int) bool { return mgr.IsStateLight(i) }
	lights.Instantiate(sc, skip)

	countUplights := func() int {
		n := 0
		for i, l := range sc.Lights {
			if !l.Interactive || mgr.IsStateLight(i) {
				continue
			}
			if _, ok := sc.LightInteractIndex(i); !ok {
				t.Fatalf("interactive light[%d] missing lightInteract entry", i)
			}
			n++
		}
		return n
	}
	if before := countUplights(); before != 3 {
		t.Fatalf("interactive uplights = %d, want 3", before)
	}

	var switchIA = -1
	for i, ia := range sc.Interactables {
		if ia.Hint == "light switch" {
			switchIA = i
			break
		}
	}
	if err := mgr.HandleInteract(sc, &sc.Interactables[switchIA]); err != nil {
		t.Fatal(err)
	}
	if mgr.StructChanged() {
		lights.Rebind(sc, skip)
	}
	if after := countUplights(); after != 3 {
		t.Fatalf("interactive uplights after ceiling off = %d, want 3", after)
	}
	if err := mgr.HandleInteract(sc, &sc.Interactables[switchIA]); err != nil {
		t.Fatal(err)
	}
	if mgr.StructChanged() {
		lights.Rebind(sc, skip)
	}
	if again := countUplights(); again != 3 {
		t.Fatalf("interactive uplights after ceiling on = %d, want 3", again)
	}
}
