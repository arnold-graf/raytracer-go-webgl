package camera

import "raytracer/internal/vec"

// FOVScale matches internal/webgpu (tan(60°/2)).
const FOVScale = 0.5773502691896257

// ProjectPoint maps a world point to pixel coordinates for HUD overlays.
func (c *Camera) ProjectPoint(rw, rh int, p vec.V) (sx, sy float64, ok bool) {
	fwd, right, up := c.Basis()
	rel := p.Sub(c.Pos)
	depth := rel.Dot(fwd)
	if depth < 0.05 {
		return 0, 0, false
	}
	aspect := float64(rw) / float64(rh)
	u := rel.Dot(right) / (depth * FOVScale * aspect)
	v := rel.Dot(up) / (depth * FOVScale)
	if u < -1.25 || u > 1.25 || v < -1.25 || v > 1.25 {
		return 0, 0, false
	}
	sx = (u + 1) * 0.5 * float64(rw)
	sy = (1 - v) * 0.5 * float64(rh)
	return sx, sy, true
}
