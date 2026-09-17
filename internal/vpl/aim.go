package vpl

import (
	"math"

	"raytracer/internal/bvh"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// aimer points an emitter's photons at the scene instead of at the whole sphere.
//
// Firing uniformly over 4*pi is correct and useless for anything far away.
// office-sunset's sun sits 226 m from the skyway, where the bridge's floor
// subtends 0.037% of the sphere: out of 512 photons, **0.19** were expected to
// land on it. The bounce that should light its ceiling was not dim, it was never
// sampled — and the same arithmetic is why that scene came out nine times short
// on the view facing the sun-lit wall.
//
// The fix is ordinary importance sampling. Enclose the scene in a sphere, fire
// only into the cone that subtends it, and scale each photon by the fraction of
// the full sphere that cone covers. The expected energy deposited is unchanged —
// uniform sampling would have landed `Rays * frac` photons each worth 1, this
// lands `Rays` photons each worth `frac` — so it is the same estimator with a
// far smaller variance, and no constant anywhere needs retuning.
//
// An emitter inside the sphere keeps the full sphere: there is no cone to aim
// into when the scene is all around you.
type aimer struct {
	centre vec.V
	radius float64
	ok     bool
}

// newAimer builds the aiming sphere from the scene's boundable geometry.
//
// Planes are excluded on purpose. They are infinite, so no finite sphere
// contains them, and including their extent would defeat the aiming entirely.
// A photon aimed at the geometry still hits the floor plane below it on the way.
func newAimer(s *scene.Scene, accel *bvh.BVH) aimer {
	lo, hi, ok := accel.Bounds()
	if !ok {
		return aimer{}
	}
	c := lo.Add(hi).Scale(0.5)
	r := hi.Sub(lo).Len() * 0.5
	if r <= 0 {
		return aimer{}
	}
	return aimer{centre: c, radius: r, ok: true}
}

// sample returns a direction and the fraction of the full sphere it stands for.
func (a aimer) sample(from vec.V, rng *pcg) (vec.V, float64) {
	if !a.ok {
		return sampleSphere(rng), 1
	}
	to := a.centre.Sub(from)
	d2 := to.LenSq()
	if d2 <= a.radius*a.radius {
		// Inside the scene sphere: everything is a potential target.
		return sampleSphere(rng), 1
	}
	d := math.Sqrt(d2)
	sinMax := a.radius / d
	cosMax := math.Sqrt(math.Max(0, 1-sinMax*sinMax))
	axis := to.Scale(1 / d)
	// Solid angle of the cone is 2*pi*(1-cosMax); the sphere's is 4*pi.
	frac := (1 - cosMax) * 0.5
	if frac <= 0 {
		return sampleSphere(rng), 1
	}
	return sampleCone(axis, cosMax, rng), frac
}
