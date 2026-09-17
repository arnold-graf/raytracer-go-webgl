package vpl

import (
	"raytracer/internal/bvh"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// kindPlane extends bvh's kind codes for the one surface class the BVH does not
// hold. Planes are unbounded, so they are tested linearly the way
// internal/probe does it.
const kindPlane = 100

type planeHit struct {
	t   float64
	idx int
}

// nearestPlane returns the closest plane along r, if any. Planes are the villa's
// floors and the office's walls in several scenes, so a photon tracer that
// skipped them would miss most of the bounce indoors.
func nearestPlane(s *scene.Scene, r vec.Ray) (planeHit, bool) {
	best := planeHit{t: scene.Inf, idx: -1}
	for i := range s.Planes {
		if t := s.Planes[i].Intersect(r); t < best.t {
			best = planeHit{t: t, idx: i}
		}
	}
	return best, best.idx >= 0
}

// surfaceAt resolves what a photon landed on: the shading attributes and the
// world-space normal.
//
// Normals are evaluated in the primitive's local space and rotated back, which
// is the same order the shader's surface_at uses. Doing it in world space is
// wrong for anything with a Xform and shows up as bounce leaving a rotated
// surface sideways.
//
// Terrain and water are deliberately absent, matching internal/probe: neither is
// in the BVH, and marching terrain for every photon would cost more than the
// bounce it finds. On an outdoor scene that means ground bounce is missing — see
// docs/vpl.md.
func surfaceAt(s *scene.Scene, kind, idx int, hit vec.V, r vec.Ray, t float64) (scene.Surface, vec.V, bool) {
	switch kind {
	case bvh.KindSphere:
		o := &s.Spheres[idx]
		lp := o.Xform.ToLocal(hit)
		return o.Surface, o.Xform.WorldNormal(o.Normal(lp)), true
	case bvh.KindBox:
		o := &s.Boxes[idx]
		lp := o.Xform.ToLocal(hit)
		return o.Surface, o.Xform.WorldNormal(o.Normal(lp)), true
	case bvh.KindCylinder:
		o := &s.Cylinders[idx]
		lr := o.Xform.LocalRay(r)
		lp := o.Xform.ToLocal(hit)
		return o.Surface, o.Xform.WorldNormal(o.Normal(lp, lr, t)), true
	case bvh.KindCone:
		o := &s.Cones[idx]
		lr := o.Xform.LocalRay(r)
		lp := o.Xform.ToLocal(hit)
		return o.Surface, o.Xform.WorldNormal(o.Normal(lp, lr, t)), true
	case bvh.KindRing:
		o := &s.Rings[idx]
		lp := o.Xform.ToLocal(hit)
		return o.Surface, o.Xform.WorldNormal(o.Normal(lp)), true
	case bvh.KindLens:
		o := &s.Lenses[idx]
		lp := o.Xform.ToLocal(hit)
		return o.Surface, o.Xform.WorldNormal(o.Normal(lp)), true
	case bvh.KindTorus:
		// Tori in these scenes are lamp rings and other emissive trim, which the
		// diffuse test below would reject anyway. Skipping them keeps this file
		// from needing a normal the CPU side does not otherwise have.
		return scene.Surface{}, vec.V{}, false
	case kindPlane:
		o := &s.Planes[idx]
		sf := o.Surface
		// A checker plane's two colours are a large part of what it bounces, so
		// take the albedo the renderer would actually shade with rather than the
		// base one.
		sf.Albedo = o.AlbedoAt(hit)
		return sf, o.N, true
	}
	return scene.Surface{}, vec.V{}, false
}
