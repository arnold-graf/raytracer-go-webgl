package joltphys

import (
	"math"

	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// collisionConfig mirrors the player movement fields used for the capsule.
type collisionConfig struct {
	EyeHeight float64
	Radius    float64
	Step      float64
}

// World is a Jolt physics scene built from a loaded level plus a player character.
type World struct {
	ps        *jolt.PhysicsSystem
	bi        *jolt.BodyInterface
	shapes    []ownedShape
	bodies    []ownedBody
	character *jolt.CharacterVirtual
	capsule   *jolt.Shape
	cfg       collisionConfig
	fallback  *scene.Scene
	bindings  bodyMap
}

// NewWorldFromScene builds static colliders from sc and spawns the player at eye.
func NewWorldFromScene(sc *scene.Scene, eye vec.V, cfg camera.Config) (*World, error) {
	if err := requireInit(); err != nil {
		return nil, err
	}
	ps := jolt.NewPhysicsSystem()
	w := &World{
		ps:       ps,
		bi:       ps.GetBodyInterface(),
		fallback: sc,
		cfg: collisionConfig{
			EyeHeight: cfg.EyeHeight,
			Radius:    cfg.CollisionRadius,
			Step:      cfg.StepHeight,
		},
	}
	if err := w.buildFromScene(sc); err != nil {
		w.Destroy()
		return nil, err
	}
	w.buildDynamicFromScene(sc)
	w.attachDetachedBoxes(sc)
	w.buildDoorBodies(sc)
	if err := w.spawnPlayer(eye, cfg); err != nil {
		w.Destroy()
		return nil, err
	}
	return w, nil
}

func (w *World) spawnPlayer(eye vec.V, cfg camera.Config) error {
	halfH, radius := capsuleOffsets(collisionConfig{EyeHeight: cfg.EyeHeight, Radius: cfg.CollisionRadius})
	w.capsule = jolt.CreateCapsule(halfH, radius)
	settings := jolt.NewCharacterVirtualSettings(w.capsule)
	settings.MaxSlopeAngle = jolt.DegreesToRadians(50)
	settings.Mass = characterMassKg
	settings.MaxStrength = characterMaxStrength
	w.character = w.ps.CreateCharacterVirtual(settings, characterFromEye(eye, w.cfg))
	return nil
}

// SyncPlayer teleports the character to match a camera pose (spawn, portal, noclip exit).
func (w *World) SyncPlayer(eye vec.V) {
	if w == nil || w.character == nil {
		return
	}
	w.character.SetPosition(characterFromEye(eye, w.cfg))
	w.character.SetLinearVelocity(jolt.Vec3{})
}

// Destroy frees all Jolt resources owned by the world.
func (w *World) Destroy() {
	if w == nil {
		return
	}
	if w.character != nil {
		w.character.Destroy()
		w.character = nil
	}
	if w.capsule != nil {
		w.capsule.Destroy()
		w.capsule = nil
	}
	for _, b := range w.bodies {
		if b.body != nil {
			b.body.Destroy()
		}
	}
	w.bodies = nil
	for _, s := range w.shapes {
		if s.shape != nil {
			s.shape.Destroy()
		}
	}
	w.shapes = nil
	if w.ps != nil {
		w.ps.Destroy()
		w.ps = nil
	}
}

// UpdatePlayer drives the Jolt character from camera input and writes the eye
// position back into cam.
func (w *World) UpdatePlayer(cam *camera.Camera, m camera.Move, dt float64) {
	if w == nil || w.character == nil || cam == nil {
		return
	}
	if cam.NoClip {
		cam.Update(m, dt)
		w.SyncPlayer(cam.Pos)
		return
	}

	cfg := cam.Config()
	eyeH, speedMul := cam.TickStance(m)

	fwd, strafe := movementAxes(m)
	sy, cy := math.Sin(cam.Yaw), math.Cos(cam.Yaw)
	speed := cfg.WalkSpeed * speedMul
	moveX := (strafe*cy - fwd*sy) * speed
	moveZ := (-strafe*sy - fwd*cy) * speed

	var vel jolt.Vec3
	if w.character.IsSupported() {
		vel = w.character.GetGroundVelocity()
	} else {
		vel = w.character.GetLinearVelocity()
	}
	vel.X = float32(moveX)
	vel.Z = float32(moveZ)

	if m.Jump && w.character.IsSupported() {
		vel.Y = float32(cfg.JumpVelocity)
	} else if !w.character.IsSupported() {
		vel.Y -= float32(cfg.Gravity * dt)
	}

	w.character.SetLinearVelocity(vel)
	// Game gravity is per fixed tick (dt); convert to m/s² for Jolt's integrator.
	gravity := jolt.Vec3{Y: float32(-cfg.Gravity / dt)}
	w.character.ExtendedUpdate(float32(dt), gravity)
	w.applyWalkPush(vel, float32(dt))
	w.ps.Update(float32(dt))
	w.SyncDynamicPoses(w.fallback)

	pos := w.character.GetPosition()
	cam.SetPos(eyeFromCharacter(pos, collisionConfig{EyeHeight: eyeH, Radius: cfg.CollisionRadius}))
	cam.SetOnGround(w.character.IsSupported())
}

func movementAxes(m camera.Move) (fwd, strafe float64) {
	if m.Forward {
		fwd++
	}
	if m.Back {
		fwd--
	}
	if m.Right {
		strafe++
	}
	if m.Left {
		strafe--
	}
	fwd += m.MoveZ
	strafe += m.MoveX
	return clampMag(fwd, strafe, 1)
}

// GroundHeight implements camera.World using Jolt raycasts, falling back to the scene.
func (w *World) GroundHeight(x, z, headY float64) float64 {
	if h, ok := w.groundRay(x, z, headY, math.Inf(1)); ok {
		return h
	}
	if w.fallback != nil {
		return w.fallback.GroundHeight(x, z, headY)
	}
	return 0
}

// WalkGroundHeight implements camera.World.
func (w *World) WalkGroundHeight(x, z, feetY, headY, step float64) float64 {
	if h, ok := w.groundRay(x, z, headY, feetY+step); ok {
		return h
	}
	if w.fallback != nil {
		return w.fallback.WalkGroundHeight(x, z, feetY, headY, step)
	}
	return feetY
}

// Blocked implements camera.World using a capsule overlap test.
func (w *World) Blocked(x, z, feetY, headY, radius, step float64) bool {
	if w == nil || w.ps == nil {
		return false
	}
	halfH, capR := capsuleOffsets(collisionConfig{EyeHeight: headY - feetY, Radius: radius})
	shape := jolt.CreateCapsule(halfH, float32(capR))
	defer shape.Destroy()
	centerY := feetY + float64(halfH+capR)
	pos := jolt.Vec3{X: float32(x), Y: float32(centerY), Z: float32(z)}
	if w.ps.CollideShape(shape, pos, 0) {
		return true
	}
	if step > 0 {
		pos.Y += float32(step)
		return w.ps.CollideShape(shape, pos, 0)
	}
	return false
}

func (w *World) groundRay(x, z, headY, maxFeetY float64) (float64, bool) {
	if w == nil || w.ps == nil {
		return 0, false
	}
	origin := jolt.Vec3{X: float32(x), Y: float32(headY + 2), Z: float32(z)}
	dir := jolt.Vec3{Y: float32(-(headY + 4))}
	hit, ok := w.ps.CastRay(origin, dir)
	if !ok {
		return 0, false
	}
	if float64(hit.HitPoint.Y) > maxFeetY {
		return 0, false
	}
	return float64(hit.HitPoint.Y), true
}

// BodyCount returns the number of static bodies (for tests).
func (w *World) BodyCount() int {
	if w == nil {
		return 0
	}
	return len(w.bodies)
}

var _ camera.World = (*World)(nil)
