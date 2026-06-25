package app

import (
	"math"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
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

func TestPortalCaptureOriginUsesSavedForward(t *testing.T) {
	saved := camera.Pose{Pos: vec.New(0, 1.6, 0), Yaw: 0, Pitch: 0}
	origin := portalCaptureOrigin(saved, 1.25)
	if math.Abs(origin.Pos.Z-1.25) > 1e-9 || origin.Pos.X != 0 || origin.Pos.Y != 1.6 {
		t.Fatalf("origin pos = %v, want (0, 1.6, 1.25)", origin.Pos)
	}
	// Left/right shots must share the same origin; only yaw changes.
	left := origin
	left.Yaw += 60 * math.Pi / 180
	right := origin
	right.Yaw -= 60 * math.Pi / 180
	if left.Pos != origin.Pos || right.Pos != origin.Pos {
		t.Fatalf("side shots moved origin: left=%v right=%v", left.Pos, right.Pos)
	}
}

func TestPullBackPortalCapture(t *testing.T) {
	cam := camera.New()
	cam.Pos = vec.New(0, 1.6, 0)
	cam.Yaw = 0
	cam.Pitch = 0
	pullBackPortalCapture(cam, 1.25)
	if math.Abs(cam.Pos.Z-1.25) > 1e-9 || cam.Pos.X != 0 || cam.Pos.Y != 1.6 {
		t.Fatalf("pullback pos = %v, want (0, 1.6, 1.25)", cam.Pos)
	}
}
