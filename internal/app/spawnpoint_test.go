package app

import (
	"math"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestApplySpawnpointFloor(t *testing.T) {
	cam := camera.New()
	sp := scene.Point{
		Pos: vec.New(1.5, 0, 1.5), Yaw: 0.2, Pitch: -0.1,
		UseFloor: true, FloorY: 0.3,
	}
	applyPlayerAtPoint(cam, sp)
	if math.Abs(cam.Pos.X-1.5) > 1e-9 || math.Abs(cam.Pos.Z-1.5) > 1e-9 {
		t.Fatalf("pos xz = %v", cam.Pos)
	}
	wantY := 0.3 + cam.EyeHeight()
	if math.Abs(cam.Pos.Y-wantY) > 1e-9 {
		t.Fatalf("eye y = %v, want %v", cam.Pos.Y, wantY)
	}
	if math.Abs(cam.Yaw-0.2) > 1e-9 || math.Abs(cam.Pitch+0.1) > 1e-9 {
		t.Fatalf("yaw/pitch = %v/%v", cam.Yaw, cam.Pitch)
	}
}

func TestApplySpawnpointEyePos(t *testing.T) {
	cam := camera.New()
	sp := scene.Point{
		Pos: vec.New(0, 2.0, 28), Yaw: 0.5, Pitch: 0.16,
	}
	applyPlayerAtPoint(cam, sp)
	if cam.Pos != sp.Pos {
		t.Fatalf("pos = %v, want %v", cam.Pos, sp.Pos)
	}
}
