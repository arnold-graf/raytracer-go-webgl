package screen

import (
	"math"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestViewPoseFacesScreen(t *testing.T) {
	box := scene.Box{
		Min: vec.New(-0.14, -0.115, -0.0005),
		Max: vec.New(0.14, 0.115, 0.0005),
		Surface: scene.Surface{
			Xform: scene.NewRigidTransform(-10, 0, 0, vec.New(0.2, 1.2, 0.5)),
		},
	}
	pose := ViewPose(&box, 16.0/9.0)
	fwd := poseForward(pose)
	normal := box.Surface.Xform.RotateDir(vec.V{Z: 1}).Normalize()
	dot := fwd.Dot(normal.Neg())
	if dot < 0.95 {
		t.Fatalf("camera should face screen, dot=%.3f", dot)
	}
	if pose.Pos.Sub(box.Surface.Xform.ToWorld(vec.V{})).Len() < 0.1 {
		t.Fatal("camera too close to screen center")
	}
}

func poseForward(p camera.Pose) vec.V {
	sy, cy := math.Sin(p.Yaw), math.Cos(p.Yaw)
	cp := math.Cos(p.Pitch)
	return vec.V{X: -sy * cp, Y: math.Sin(p.Pitch), Z: -cy * cp}
}
