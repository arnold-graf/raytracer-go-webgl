package character

import (
	"math"
	"sort"

	"raytracer/internal/vec"
)

const spiderSupportMargin = 0.14

// convexHullXZ returns a CCW convex hull of points projected on the XZ plane.
func convexHullXZ(points []vec.V) []vec.V {
	if len(points) <= 2 {
		return append([]vec.V(nil), points...)
	}
	pts := append([]vec.V(nil), points...)
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].X != pts[j].X {
			return pts[i].X < pts[j].X
		}
		return pts[i].Z < pts[j].Z
	})

	cross := func(o, a, b vec.V) float64 {
		return (a.X-o.X)*(b.Z-o.Z) - (a.Z-o.Z)*(b.X-o.X)
	}

	var lower []vec.V
	for _, p := range pts {
		for len(lower) >= 2 && cross(lower[len(lower)-2], lower[len(lower)-1], p) <= 0 {
			lower = lower[:len(lower)-1]
		}
		lower = append(lower, p)
	}
	var upper []vec.V
	for i := len(pts) - 1; i >= 0; i-- {
		p := pts[i]
		for len(upper) >= 2 && cross(upper[len(upper)-2], upper[len(upper)-1], p) <= 0 {
			upper = upper[:len(upper)-1]
		}
		upper = append(upper, p)
	}
	hull := append(lower[:len(lower)-1], upper[:len(upper)-1]...)
	return hull
}

func pointInConvexPolygonXZ(p vec.V, hull []vec.V, margin float64) bool {
	n := len(hull)
	if n < 3 {
		return false
	}
	inside := true
	for i := 0; i < n; i++ {
		a := hull[i]
		b := hull[(i+1)%n]
		edge := vec.V{X: b.X - a.X, Z: b.Z - a.Z}
		toP := vec.V{X: p.X - a.X, Z: p.Z - a.Z}
		cross := edge.X*toP.Z - edge.Z*toP.X
		if cross < -margin {
			inside = false
			break
		}
	}
	return inside
}

func spiderCanLift(feet []Foot, liftIdx int, com vec.V, margin float64) bool {
	var support []vec.V
	for i, f := range feet {
		if i == liftIdx || !f.Initialized || f.Phase == FootSwing {
			continue
		}
		support = append(support, f.PlantWorld)
	}
	if len(support) < 3 {
		return false
	}
	hull := convexHullXZ(support)
	return pointInConvexPolygonXZ(com, hull, margin)
}

func spiderBodyCOM(bodyPos vec.V) vec.V {
	return vec.V{X: bodyPos.X, Y: bodyPos.Y, Z: bodyPos.Z}
}

func smoothExp(alphaPerSec, dt float64) float64 {
	if dt <= 0 {
		return 1
	}
	return 1 - math.Exp(-alphaPerSec*dt)
}
