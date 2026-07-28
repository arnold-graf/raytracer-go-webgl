package physics

import (
	"math"

	"raytracer/internal/vec"
)

// FitPlane estimates a ground plane through points. Returns unit normal and centroid.
func FitPlane(points []vec.V) (normal, centroid vec.V) {
	if len(points) == 0 {
		return vec.V{Y: 1}, vec.V{}
	}
	for _, p := range points {
		centroid = centroid.Add(p)
	}
	centroid = centroid.Scale(1 / float64(len(points)))
	if len(points) < 3 {
		return vec.V{Y: 1}, centroid
	}

	var n vec.V
	for i := range points {
		a := points[i].Sub(centroid)
		b := points[(i+1)%len(points)].Sub(centroid)
		n = n.Add(a.Cross(b))
	}
	if n.LenSq() < 1e-12 {
		return vec.V{Y: 1}, centroid
	}
	n = n.Normalize()
	if n.Y < 0 {
		n = n.Scale(-1)
	}
	return n, centroid
}

// HoverForce returns a spring-damper holding the body at restHeight above plane.
// Gravity is not applied separately — the spring provides full support.
func HoverForce(body *Body, planePoint, planeNormal vec.V, restHeight, k, damp float64) vec.V {
	if body == nil {
		return vec.V{}
	}
	target := planePoint.Add(planeNormal.Scale(restHeight))
	err := target.Sub(body.Pos)
	errN := planeNormal.Scale(err.Dot(planeNormal))
	// Prevent runaway spring force when the body has drifted far from the foot plane.
	if hl := math.Abs(errN.Y); hl > 1.5 {
		errN = planeNormal.Scale(1.5 * math.Copysign(1, errN.Y))
	}
	velN := planeNormal.Scale(body.Vel.Dot(planeNormal))
	return errN.Scale(k).Sub(velN.Scale(damp))
}

// OrientTorque returns pitch/roll torque aligning body up with targetNormal.
func OrientTorque(body *Body, targetNormal vec.V, k, damp float64) vec.V {
	if body == nil || targetNormal.LenSq() < 1e-12 {
		return vec.V{}
	}
	targetNormal = targetNormal.Normalize()
	current := body.BodyUp()
	if current.LenSq() < 1e-12 {
		current = vec.V{Y: 1}
	} else {
		current = current.Normalize()
	}
	axis := current.Cross(targetNormal)
	if axis.LenSq() < 1e-12 {
		return vec.V{}
	}
	torque := axis.Scale(k)
	torque.Y = 0 // never spin on yaw from balance torque
	torque = torque.Sub(vec.V{X: body.AngVel.X * damp, Z: body.AngVel.Z * damp})
	return torque
}

// ClampTilt limits pitch/roll after integration.
func ClampTilt(body *Body, maxDeg float64) {
	if body == nil {
		return
	}
	body.Pitch = clamp(body.Pitch, -maxDeg, maxDeg)
	body.Roll = clamp(body.Roll, -maxDeg, maxDeg)
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
