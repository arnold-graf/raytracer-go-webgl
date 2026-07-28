package scene

import (
	"math"

	"raytracer/internal/vec"
)

// SurfaceHit is the result of a scene ray or sphere cast.
type SurfaceHit struct {
	Point  vec.V
	Normal vec.V
	Hit    bool
	Dist   float64
}

// CastRayStatic finds the nearest walkable surface hit along ray within maxDist,
// ignoring dynamic NPC geometry.
func (s *Scene) CastRayStatic(ray vec.Ray, maxDist float64) SurfaceHit {
	return s.castRay(ray, maxDist, s.isDynamicBox, s.isDynamicCylinder, s.isDynamicSphere)
}

// CastRay is like CastRayStatic but includes dynamic geometry.
func (s *Scene) CastRay(ray vec.Ray, maxDist float64) SurfaceHit {
	return s.castRay(ray, maxDist, nil, nil, nil)
}

func (s *Scene) castRay(ray vec.Ray, maxDist float64, skipBox, skipCylinder, skipSphere func(int) bool) SurfaceHit {
	if s == nil || maxDist <= 0 {
		return SurfaceHit{}
	}
	if ray.Dir.LenSq() < 1e-18 {
		return SurfaceHit{}
	}
	ray.Dir = ray.Dir.Normalize()

	best := SurfaceHit{Dist: math.Inf(1)}
	try := func(t float64, p, n vec.V) {
		if t < eps || t > maxDist || t >= best.Dist {
			return
		}
		if n.LenSq() < 1e-12 {
			n = vec.V{Y: 1}
		} else {
			n = n.Normalize()
		}
		best = SurfaceHit{Point: p, Normal: n, Hit: true, Dist: t}
	}

	for i := range s.Terrains {
		t := s.Terrains[i].IntersectWithin(ray, maxDist)
		if math.IsInf(t, 1) {
			continue
		}
		p := ray.At(t)
		try(t, p, s.Terrains[i].Normal(p))
	}

	for i := range s.Spheres {
		if skipSphere != nil && skipSphere(i) {
			continue
		}
		sp := &s.Spheres[i]
		lr := ray
		if sp.Xform != nil {
			lr = sp.Xform.LocalRay(ray)
		}
		t := sp.Intersect(lr)
		if math.IsInf(t, 1) || t > maxDist {
			continue
		}
		p := lr.At(t)
		n := sp.Normal(p)
		if sp.Xform != nil {
			p = sp.Xform.ToWorld(p)
			n = sp.Xform.WorldNormal(n)
		}
		try(t, p, n)
	}

	for i := range s.Boxes {
		if skipBox != nil && skipBox(i) {
			continue
		}
		b := &s.Boxes[i]
		lr := ray
		if b.Xform != nil {
			lr = b.Xform.LocalRay(ray)
		}
		t := b.Intersect(lr)
		if math.IsInf(t, 1) || t > maxDist {
			continue
		}
		p := lr.At(t)
		n := b.Normal(p)
		if b.Xform != nil {
			p = b.Xform.ToWorld(p)
			n = b.Xform.WorldNormal(n)
		}
		try(t, p, n)
	}

	for i := range s.Cylinders {
		if skipCylinder != nil && skipCylinder(i) {
			continue
		}
		c := &s.Cylinders[i]
		lr := ray
		if c.Xform != nil {
			lr = c.Xform.LocalRay(ray)
		}
		t := c.Intersect(lr)
		if math.IsInf(t, 1) || t > maxDist {
			continue
		}
		p := lr.At(t)
		n := c.Normal(p, lr, t)
		if c.Xform != nil {
			p = c.Xform.ToWorld(p)
			n = c.Xform.WorldNormal(n)
		}
		try(t, p, n)
	}

	for i := range s.Cones {
		c := &s.Cones[i]
		lr := ray
		if c.Xform != nil {
			lr = c.Xform.LocalRay(ray)
		}
		t := c.Intersect(lr)
		if math.IsInf(t, 1) || t > maxDist {
			continue
		}
		p := lr.At(t)
		n := c.Normal(p, lr, t)
		if c.Xform != nil {
			p = c.Xform.ToWorld(p)
			n = c.Xform.WorldNormal(n)
		}
		try(t, p, n)
	}

	if !best.Hit {
		return SurfaceHit{}
	}
	return best
}

// CastSphereStatic sweeps a sphere along ray (simple: offset origin by radius toward
// hit normal after ray hit). For spider foot placement this matches Unity's
// SphereCast closely enough on our analytic primitives.
func (s *Scene) CastSphereStatic(ray vec.Ray, radius, maxDist float64) SurfaceHit {
	hit := s.CastRayStatic(ray, maxDist)
	if !hit.Hit || radius <= 0 {
		return hit
	}
	hit.Point = hit.Point.Add(hit.Normal.Scale(radius))
	return hit
}
