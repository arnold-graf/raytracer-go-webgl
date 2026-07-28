package character

import (
	"math"

	"raytracer/internal/vec"
)

func signedAngleDeg(from, to, axis vec.V) float64 {
	if from.LenSq() < 1e-12 || to.LenSq() < 1e-12 || axis.LenSq() < 1e-12 {
		return 0
	}
	from = from.Normalize()
	to = to.Normalize()
	ax := axis.Normalize()
	cross := ax.Cross(from)
	dot := clampScalar(from.Dot(to), -1, 1)
	return math.Atan2(cross.Dot(to), dot) * 180 / math.Pi
}

func rotateAround(p, pivot, axis vec.V, deg float64) vec.V {
	rad := deg * math.Pi / 180
	c, s := math.Cos(rad), math.Sin(rad)
	v := p.Sub(pivot)
	ax := axis.Normalize()
	rot := v.Scale(c).Add(ax.Cross(v).Scale(s)).Add(ax.Scale(ax.Dot(v) * (1 - c)))
	return pivot.Add(rot)
}
