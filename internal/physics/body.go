// Package physics provides a minimal rigid-body integrator for arthropod NPCs.
package physics

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

const defaultGravity = 9.81

// Body is a single rigid body with Euler orientation (pitch, yaw, roll degrees).
type Body struct {
	Pos    vec.V
	Vel    vec.V
	Pitch  float64
	Yaw    float64
	Roll   float64
	AngVel vec.V // rad/s: pitch, yaw, roll
	Mass   float64
}

// NewBody returns a body at pos with yaw (degrees).
func NewBody(pos vec.V, yawDeg float64, mass float64) Body {
	if mass <= 0 {
		mass = 1
	}
	return Body{Pos: pos, Yaw: yawDeg, Mass: mass}
}

// Transform returns the body's world rigid transform.
func (b *Body) Transform() *scene.Transform {
	if b == nil {
		return nil
	}
	return scene.NewRigidTransform(b.Pitch, b.Yaw, b.Roll, b.Pos)
}

// ApplyForce integrates linear force (world space, Newtons) over dt.
func (b *Body) ApplyForce(force vec.V, dt float64) {
	if b == nil || b.Mass <= 0 {
		return
	}
	b.Vel = b.Vel.Add(force.Scale(dt / b.Mass))
}

// ApplyTorque integrates torque (world pitch/yaw/roll, N·m) over dt.
func (b *Body) ApplyTorque(torque vec.V, dt float64) {
	if b == nil || b.Mass <= 0 {
		return
	}
	inertia := b.Mass * 0.35 // rough torso radius of gyration
	b.AngVel = b.AngVel.Add(torque.Scale(dt / inertia))
}

// Integrate advances position and orientation with velocity damping.
func (b *Body) Integrate(dt float64, linDrag, angDrag float64) {
	if b == nil {
		return
	}
	damp := math.Max(0, 1-linDrag*dt)
	b.Vel = b.Vel.Scale(damp)
	b.Pos = b.Pos.Add(b.Vel.Scale(dt))

	angDamp := math.Max(0, 1-angDrag*dt)
	b.AngVel = b.AngVel.Scale(angDamp)
	b.AngVel.Y = 0 // yaw is nav-driven, not physics
	b.Pitch += b.AngVel.X * dt * 180 / math.Pi
	b.Roll += b.AngVel.Z * dt * 180 / math.Pi
}

// ClampSpeed limits linear speed (m/s).
func ClampSpeed(body *Body, maxSpeed float64) {
	if body == nil || maxSpeed <= 0 {
		return
	}
	if sp := body.Vel.Len(); sp > maxSpeed {
		body.Vel = body.Vel.Scale(maxSpeed / sp)
	}
}

// GravityForce returns downward weight.
func GravityForce(mass float64) vec.V {
	return vec.V{Y: -defaultGravity * mass}
}

// BodyUp returns the world-space up direction of the body.
func (b *Body) BodyUp() vec.V {
	xf := b.Transform()
	return xf.WorldNormal(vec.V{Y: 1})
}
