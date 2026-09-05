package app

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

// The HUD position is meant to be pasted straight into a scene TOML or into
// cmd/gpuprof's -cam-x/-cam-y/-cam-z, so it must print x, y, z in that order.
// It used to print x, z, y, which silently sent anyone reading a position off
// the screen to a different place in the scene.
func TestFormatHUDPos(t *testing.T) {
	got := formatHUDPos(vec.New(41.14, 200.0, 8.91), math.Pi/2, -0.25)
	want := "[41.1, 200.0, 8.9] yaw 90.0 pitch -14.32"
	if got != want {
		t.Fatalf("formatHUDPos = %q, want %q", got, want)
	}
}
