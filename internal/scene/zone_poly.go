package scene

import (
	"fmt"
	"math"

	"raytracer/internal/vec"
)

// ZoneRectVertices builds a CCW convex quad from a centered rectangle in XZ.
// center is [x, z]; half is [half_x, half_z]; angle is rotation about Y in radians.
func ZoneRectVertices(center, half [2]float64, angle float64) []vec.V {
	cx, cz := center[0], center[1]
	hx, hz := half[0], half[1]
	c, s := math.Cos(angle), math.Sin(angle)
	local := [][2]float64{
		{-hx, -hz},
		{hx, -hz},
		{hx, hz},
		{-hx, hz},
	}
	out := make([]vec.V, len(local))
	for i, p := range local {
		lx, lz := p[0], p[1]
		out[i] = vec.V{
			X: cx + lx*c + lz*s,
			Z: cz - lx*s + lz*c,
		}
	}
	return out
}

// ZonePathVertices builds a quad for one straight path segment (two [x,z] points).
func ZonePathVertices(path [][2]float64, width float64) ([]vec.V, error) {
	if len(path) != 2 {
		return nil, fmt.Errorf("terrain.zone path segment needs exactly 2 points")
	}
	if width <= 0 {
		return nil, fmt.Errorf("terrain.zone width must be > 0")
	}
	half := width * 0.5
	a := vec.V{X: path[0][0], Z: path[0][1]}
	b := vec.V{X: path[1][0], Z: path[1][1]}
	tangent := b.Sub(a).Normalize()
	if tangent.LenSq() == 0 {
		return nil, fmt.Errorf("terrain.zone path has zero-length segment")
	}
	perp := vec.V{X: -tangent.Z, Z: tangent.X}
	left0 := a.Add(perp.Scale(half))
	left1 := b.Add(perp.Scale(half))
	right1 := b.Sub(perp.Scale(half))
	right0 := a.Sub(perp.Scale(half))
	return []vec.V{left0, left1, right1, right0}, nil
}
