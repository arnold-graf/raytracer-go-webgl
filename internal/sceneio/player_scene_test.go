package sceneio

import (
	"os"
	"testing"

	"raytracer/internal/camera"
)

func TestScenePlayerMovementOverrides(t *testing.T) {
	data := []byte(`
[player.movement]
gravity = 0.11
double_jump_enabled = true
`)
	sc, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sc.PlayerMovement.Gravity != 0.11 {
		t.Fatalf("gravity = %v, want 0.11", sc.PlayerMovement.Gravity)
	}
	if sc.PlayerMovement.DoubleJumpEnabled == nil || !*sc.PlayerMovement.DoubleJumpEnabled {
		t.Fatalf("double_jump_enabled = %v, want true", sc.PlayerMovement.DoubleJumpEnabled)
	}

	base := camera.DefaultConfig()
	got := camera.MergeConfig(base, sc.PlayerMovement)
	if got.Gravity != 0.11 {
		t.Fatalf("merged gravity = %v, want 0.11", got.Gravity)
	}
	if !got.DoubleJumpEnabled {
		t.Fatal("merged double_jump_enabled = false, want true")
	}
}

func TestSceneExtendsPlayerMovementOverride(t *testing.T) {
	dir := t.TempDir()
	basePath := dir + "/base.toml"
	childPath := dir + "/child.toml"
	if err := os.WriteFile(basePath, []byte(`
[player.movement]
gravity = 0.30
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, []byte(`
extends = "base.toml"

[player.movement]
gravity = 0.08
`), 0o644); err != nil {
		t.Fatal(err)
	}
	sc, err := Load(childPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sc.PlayerMovement.Gravity != 0.08 {
		t.Fatalf("gravity = %v, want 0.08", sc.PlayerMovement.Gravity)
	}
}
