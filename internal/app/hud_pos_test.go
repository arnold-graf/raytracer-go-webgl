package app

import (
	"testing"

	"raytracer/internal/vec"
)

func TestFormatHUDPos(t *testing.T) {
	got := formatHUDPos(vec.New(41.14, 200.0, 8.91))
	want := "[41.1, 8.9, 200.0]"
	if got != want {
		t.Fatalf("formatHUDPos = %q, want %q", got, want)
	}
}
