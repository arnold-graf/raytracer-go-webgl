package camera

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const (
	// PreviewViewCount is the number of evenly spaced orbit screenshots preview renders.
	PreviewViewCount = 12
	previewViewFill  = 0.85
	previewVFovRad   = 60 * math.Pi / 180
	// PreviewElevationRad tilts the orbit ring above the horizon (pleasant 3/4 view).
	PreviewElevationRad = 25 * math.Pi / 180
)

// PreviewSubjectBounds returns the world-space AABB of geometry preview should
// frame. Thin horizontal slabs (preview floors) are skipped so the included
// object stays centered instead of the whole ground plane.
func PreviewSubjectBounds(s *scene.Scene) (min, max vec.V, ok bool) {
	if s == nil {
		return vec.V{}, vec.V{}, false
	}
	first := true
	accum := func(omin, omax vec.V) {
		if first {
			min, max = omin, omax
			first = false
			return
		}
		min = minV(min, omin)
		max = maxV(max, omax)
	}
	for i := range s.Spheres {
		o := &s.Spheres[i]
		omin, omax := o.WorldBounds()
		accum(omin, omax)
	}
	for i := range s.Boxes {
		o := &s.Boxes[i]
		omin, omax := o.WorldBounds()
		if previewGroundBox(omin, omax) {
			continue
		}
		accum(omin, omax)
	}
	for i := range s.Cylinders {
		o := &s.Cylinders[i]
		r := o.MaxRadius()
		accum(expandBounds(o.Xform,
			vec.V{X: o.CX - r, Y: o.YMin, Z: o.CZ - r},
			vec.V{X: o.CX + r, Y: o.YMax, Z: o.CZ + r}))
	}
	for i := range s.Cones {
		o := &s.Cones[i]
		accum(expandBounds(o.Xform,
			vec.V{X: o.CX - o.RBase, Y: o.YBase, Z: o.CZ - o.RBase},
			vec.V{X: o.CX + o.RBase, Y: o.YTip, Z: o.CZ + o.RBase}))
	}
	for i := range s.Tori {
		o := &s.Tori[i]
		rxz := o.R + o.Rm
		accum(expandBounds(o.Xform,
			vec.V{X: o.Center.X - rxz, Y: o.Center.Y - o.Rm, Z: o.Center.Z - rxz},
			vec.V{X: o.Center.X + rxz, Y: o.Center.Y + o.Rm, Z: o.Center.Z + rxz}))
	}
	for i := range s.Rings {
		o := &s.Rings[i]
		sh := o.Shell()
		half := o.HalfHeight()
		accum(expandBounds(o.Xform,
			vec.V{X: o.CX - o.Radius - sh, Y: o.CY - half - sh, Z: o.CZ - o.Radius - sh},
			vec.V{X: o.CX + o.Radius + sh, Y: o.CY + half + sh, Z: o.CZ + o.Radius + sh}))
	}
	for i := range s.Lenses {
		o := &s.Lenses[i]
		omin, omax := o.WorldBounds()
		accum(omin, omax)
	}
	if first {
		return vec.V{}, vec.V{}, false
	}
	return min, max, true
}

func previewGroundBox(omin, omax vec.V) bool {
	h := omax.Y - omin.Y
	if h > 0.06 {
		return false
	}
	wx := omax.X - omin.X
	wz := omax.Z - omin.Z
	if wx*wz < 1.5 {
		return false
	}
	return omin.Y < 0.15
}

// PreviewOrbitDirections returns unit vectors from the subject center toward the
// camera. Index 0 is the front view (−Z toward a subject facing −Z).
func PreviewOrbitDirections(n int, elevRad float64) []vec.V {
	if n <= 0 {
		return nil
	}
	dirs := make([]vec.V, n)
	ce := math.Cos(elevRad)
	se := math.Sin(elevRad)
	for i := 0; i < n; i++ {
		az := math.Pi + float64(i)*2*math.Pi/float64(n)
		dirs[i] = vec.V{
			X: ce * math.Sin(az),
			Y: se,
			Z: ce * math.Cos(az),
		}.Normalize()
	}
	return dirs
}

// OrbitPose returns a camera pose on an orbit around bounds, looking at the
// center and far enough back that the subject nearly fills the viewport.
func OrbitPose(boundsMin, boundsMax vec.V, camDir vec.V, aspect float64) Pose {
	center := boundsMin.Add(boundsMax).Scale(0.5)
	camDir = camDir.Normalize()
	look := camDir.Neg()

	if aspect <= 0 {
		aspect = 16.0 / 9.0
	}

	radius := 0.0
	for _, c := range boundsCorners(boundsMin, boundsMax) {
		if d := c.Sub(center).Len(); d > radius {
			radius = d
		}
	}
	if radius < 0.05 {
		radius = 0.5
	}

	hFov := 2 * math.Atan(math.Tan(previewVFovRad/2)*aspect)
	distH := radius / math.Tan(previewViewFill*previewVFovRad/2)
	distW := radius / math.Tan(previewViewFill*hFov/2)
	dist := math.Max(distH, distW)
	if dist < 0.12 {
		dist = 0.12
	}

	pos := center.Add(camDir.Scale(dist))
	yaw := math.Atan2(-look.X, -look.Z)
	pitch := math.Asin(math.Max(-1, math.Min(1, look.Y)))
	return Pose{Pos: pos, Yaw: yaw, Pitch: pitch}
}

func boundsCorners(min, max vec.V) [8]vec.V {
	return [8]vec.V{
		min,
		{X: max.X, Y: min.Y, Z: min.Z},
		{X: min.X, Y: max.Y, Z: min.Z},
		{X: min.X, Y: min.Y, Z: max.Z},
		max,
		{X: max.X, Y: max.Y, Z: min.Z},
		{X: max.X, Y: min.Y, Z: max.Z},
		{X: min.X, Y: max.Y, Z: max.Z},
	}
}

func expandBounds(xf *scene.Transform, lmin, lmax vec.V) (vec.V, vec.V) {
	if xf == nil {
		return lmin, lmax
	}
	wmin, wmax := lmin, lmax
	for _, c := range boundsCorners(lmin, lmax) {
		w := xf.ToWorld(c)
		wmin = minV(wmin, w)
		wmax = maxV(wmax, w)
	}
	return wmin, wmax
}

func minV(a, b vec.V) vec.V {
	return vec.V{
		X: math.Min(a.X, b.X),
		Y: math.Min(a.Y, b.Y),
		Z: math.Min(a.Z, b.Z),
	}
}

func maxV(a, b vec.V) vec.V {
	return vec.V{
		X: math.Max(a.X, b.X),
		Y: math.Max(a.Y, b.Y),
		Z: math.Max(a.Z, b.Z),
	}
}
