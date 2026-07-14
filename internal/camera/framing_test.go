package camera

import (
	"math"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestPreviewSubjectBoundsSkipsFloor(t *testing.T) {
	s := &scene.Scene{
		Boxes: []scene.Box{
			{Min: vec.New(-1.5, 0, -1.2), Max: vec.New(1.5, 0.02, 1.8)},
			{Min: vec.New(-0.2, 0.4, -0.2), Max: vec.New(0.2, 0.9, 0.2)},
		},
	}
	min, max, ok := PreviewSubjectBounds(s)
	if !ok {
		t.Fatal("expected bounds")
	}
	if max.Y-min.Y > 0.6 {
		t.Fatalf("subject height %.3f, want chair only (<0.6)", max.Y-min.Y)
	}
	if min.Y < 0.35 {
		t.Fatalf("subject min Y %.3f, want chair base above floor", min.Y)
	}
}

func TestPreviewOrbitDirectionsFront(t *testing.T) {
	dirs := PreviewOrbitDirections(PreviewViewCount, PreviewElevationRad)
	if len(dirs) != PreviewViewCount {
		t.Fatalf("got %d directions, want %d", len(dirs), PreviewViewCount)
	}
	front := dirs[0]
	if front.Z > -0.5 {
		t.Fatalf("front camera dir Z=%.3f, want negative (camera on -Z side)", front.Z)
	}
	if front.Y <= 0 {
		t.Fatalf("front camera dir Y=%.3f, want positive elevation", front.Y)
	}
}

func TestOrbitPoseLooksAtCenter(t *testing.T) {
	min := vec.New(-0.5, 0.0, -0.5)
	max := vec.New(0.5, 1.0, 0.5)
	dir := vec.New(0, math.Sin(PreviewElevationRad), -math.Cos(PreviewElevationRad)).Normalize()
	pose := OrbitPose(min, max, dir, 1.0)

	center := vec.New(0, 0.5, 0)
	fwd, _, _ := (&Camera{Pos: pose.Pos, Yaw: pose.Yaw, Pitch: pose.Pitch}).Basis()
	look := center.Sub(pose.Pos).Normalize()
	if fwd.Dot(look) < 0.99 {
		t.Fatalf("camera forward %v not aligned with look %v", fwd, look)
	}
}
