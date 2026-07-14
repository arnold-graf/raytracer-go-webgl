package anim

import (
	"math"

	"raytracer/internal/camera"
	"raytracer/internal/vec"
)

// LerpPose blends two camera poses. Position uses smooth interpolation; angles
// take the shortest path.
func LerpPose(a, b camera.Pose, t float64) camera.Pose {
	u := math.Max(0, math.Min(1, t))
	return camera.Pose{
		Pos: vec.V{
			X: a.Pos.X + (b.Pos.X-a.Pos.X)*u,
			Y: a.Pos.Y + (b.Pos.Y-a.Pos.Y)*u,
			Z: a.Pos.Z + (b.Pos.Z-a.Pos.Z)*u,
		},
		Yaw:   lerpAngle(a.Yaw, b.Yaw, u),
		Pitch: lerpAngle(a.Pitch, b.Pitch, u),
	}
}

func lerpAngle(a, b, t float64) float64 {
	d := math.Remainder(b-a, 2*math.Pi)
	return a + d*t
}
