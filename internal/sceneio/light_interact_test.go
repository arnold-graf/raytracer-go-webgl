package sceneio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInteractiveLightLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lamp.toml")
	if err := os.WriteFile(path, []byte(`
[[light]]
pos = [0.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]
interactive = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Lights) != 1 {
		t.Fatalf("lights = %d", len(s.Lights))
	}
	l := s.Lights[0]
	if !l.Interactive {
		t.Fatal("expected interactive")
	}
	if l.Hint != "lamp" {
		t.Fatalf("hint = %q, want lamp", l.Hint)
	}
}

func TestInteractiveLightCustomHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lamp.toml")
	if err := os.WriteFile(path, []byte(`
[[light]]
pos = [0.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]
interactive = true
hint = "overhead"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Lights[0].Hint != "overhead" {
		t.Fatalf("hint = %q", s.Lights[0].Hint)
	}
}

func TestStateLightOnUseWithoutInteractive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lamp.toml")
	if err := os.WriteFile(path, []byte(`
[state]
on = true

[[light]]
pos = [0.0, 1.0, 0.0]
color = [1.0, 1.0, 1.0]
on_use = 'toggle(on)'
hint = "lamp"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Interactables) != 1 {
		t.Fatalf("interactables = %d, want 1", len(s.Interactables))
	}
	ia := s.Interactables[0]
	if ia.Handler != "state" || ia.Hint != "lamp" {
		t.Fatalf("interactable = %+v", ia)
	}
}

func TestDeskAnglepoiseLampToggleSphere(t *testing.T) {
	sc, err := Load("../../scenes/objects/desk-anglepoise-lamp.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Interactables) != 1 {
		t.Fatalf("interactables = %d, want 1", len(sc.Interactables))
	}
	ia := sc.Interactables[0]
	if ia.Handler != "state" || ia.Hint != "lamp" || ia.StateAction != "toggle(is_on)" {
		t.Fatalf("interactable = %+v", ia)
	}
}

func TestDeskLampInstancedInteractable(t *testing.T) {
	sc, err := Load("../../scenes/office-sunset/server-room-1.toml")
	if err != nil {
		t.Fatal(err)
	}
	var state int
	for _, ia := range sc.Interactables {
		if ia.Handler == "state" && ia.StateAction == "toggle(is_on)" {
			state++
		}
	}
	if state == 0 {
		t.Fatal("expected instanced desk lamp state interactable")
	}
	if sc.Reactive == nil || len(sc.Reactive.Fragments) == 0 {
		t.Fatal("expected reactive fragment for instanced desk lamp")
	}
}
