package app

import (
	"testing"

	"math"

	"raytracer/internal/texture"
)

func TestResolveScenePath(t *testing.T) {
	got := resolveScenePath("scenes/outdoors-night-villa.toml", "manhattan_city_block.toml")
	want := "scenes/manhattan_city_block.toml"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if resolveScenePath("", "foo.toml") != "scenes/foo.toml" {
		t.Fatal("empty current should default to scenes/")
	}
}

func TestCropSquareFromBuffer(t *testing.T) {
	src := make([]byte, 4*2*4)
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			off := (y*4 + x) * 4
			src[off] = byte(x*10 + y)
		}
	}
	dst := make([]byte, 2*2*4)
	cropSquareFromBuffer(dst, 2, src, 4, 2)
	if dst[0] != 10 || dst[8] != 11 {
		t.Fatalf("cropped pixels = %d,%d want 10,11", dst[0], dst[8])
	}
}

func TestPortalShotsMatchCaptureSlots(t *testing.T) {
	if len(portalShots) != len(texture.CaptureIDs) {
		t.Fatalf("portal shots %d != capture slots %d", len(portalShots), len(texture.CaptureIDs))
	}
}

func TestClampCapturePitch(t *testing.T) {
	const maxP = math.Pi/2 - 0.01
	if clampCapturePitch(maxP+1) != maxP {
		t.Fatal("expected clamp at +max")
	}
	if clampCapturePitch(-maxP-1) != -maxP {
		t.Fatal("expected clamp at -max")
	}
	if clampCapturePitch(0.5) != 0.5 {
		t.Fatal("expected unchanged in range")
	}
}
