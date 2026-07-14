package screen

import (
	"math"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	viewFill = 0.88
	vFovRad  = 60 * math.Pi / 180
)

// ViewPose returns a camera pose facing the screen's +Z face head-on, far
// enough back that the panel nearly fills the viewport.
func ViewPose(box *scene.Box, aspect float64) camera.Pose {
	if box == nil {
		return camera.Pose{}
	}
	xf := box.Xform
	center := xf.ToWorld(vec.V{})
	normal := xf.RotateDir(vec.V{Z: 1}).Normalize()

	w := box.Max.X - box.Min.X
	h := box.Max.Y - box.Min.Y
	if w <= 0 {
		w = 0.1
	}
	if h <= 0 {
		h = 0.1
	}
	if aspect <= 0 {
		aspect = 16.0 / 9.0
	}

	hFov := 2 * math.Atan(math.Tan(vFovRad/2)*aspect)
	distH := (h / 2) / math.Tan(viewFill*vFovRad/2)
	distW := (w / 2) / math.Tan(viewFill*hFov/2)
	dist := math.Max(distH, distW)
	if dist < 0.12 {
		dist = 0.12
	}

	pos := center.Add(normal.Scale(dist))
	look := center.Sub(pos).Normalize()
	yaw := math.Atan2(-look.X, -look.Z)
	pitch := math.Asin(math.Max(-1, math.Min(1, look.Y)))
	return camera.Pose{Pos: pos, Yaw: yaw, Pitch: pitch}
}
