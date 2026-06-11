// Package camera holds the first-person camera state and the movement / look /
// ray-generation math. Movement supports terrain/floor following, wall
// collisions (with a noclip override), and brisk jump physics, all tunable via
// Config.
package camera

import (
	"math"

	"raytracer/internal/vec"
)

const (
	lookGain = 0.003
	maxPitch = 1.3
)

// Config holds the tunable player-movement parameters (loaded from TOML).
type Config struct {
	WalkSpeed       float64 // horizontal speed (world units per dt)
	EyeHeight       float64 // camera height above the surface underfoot
	JumpVelocity    float64 // initial upward velocity of a jump
	Gravity         float64 // downward acceleration per dt
	CollisionRadius float64 // player's horizontal collision radius
	StepHeight      float64 // max ledge height the player steps over freely
}

// DefaultConfig returns the built-in movement tuning.
func DefaultConfig() Config {
	return Config{
		WalkSpeed:       0.35,
		EyeHeight:       1.6,
		JumpVelocity:    0.42,
		Gravity:         0.20,
		CollisionRadius: 0.30,
		StepHeight:      0.45,
	}
}

// World supplies the collision/terrain queries the camera needs to walk the
// scene. Coordinates are world-space; it is implemented by *scene.Scene.
type World interface {
	// GroundHeight returns the height of the highest walkable surface at (x,z)
	// whose top is at or below headY (so ceilings/overhangs are ignored).
	GroundHeight(x, z, headY float64) float64
	// Blocked reports whether a vertical capsule of the given radius, standing on
	// feetY with its head at headY, intersects solid geometry at (x,z). Surfaces
	// no taller than feetY+step are treated as walkable (steps), not walls.
	Blocked(x, z, feetY, headY, radius, step float64) bool
}

// Camera is a yaw/pitch first-person camera with jump physics, ground following
// and wall collisions.
type Camera struct {
	Pos    vec.V
	Yaw    float64
	Pitch  float64
	NoClip bool // when true, walk through walls (collisions disabled)

	cfg      Config
	world    World
	velY     float64
	onGround bool
}

// New returns a camera at the original starting position, facing into the scene.
func New() *Camera {
	return &Camera{Pos: vec.New(0, 1.6, 5), Yaw: 0, cfg: DefaultConfig()}
}

// SetConfig replaces the movement tuning.
func (c *Camera) SetConfig(cfg Config) { c.cfg = cfg }

// SetWorld attaches the collision/terrain world (nil = flat plane at y=0).
func (c *Camera) SetWorld(w World) { c.world = w }

// SnapToGround places the camera directly on the surface underfoot, avoiding an
// initial fall when a scene's start pose is above the ground.
func (c *Camera) SnapToGround() {
	if c.world == nil {
		return
	}
	g := c.world.GroundHeight(c.Pos.X, c.Pos.Z, c.Pos.Y+1e3)
	c.Pos.Y = g + c.cfg.EyeHeight
	c.velY = 0
	c.onGround = true
}

// Move describes which movement inputs are active this frame.
type Move struct {
	Forward, Back, Left, Right, Jump bool
}

// Update applies horizontal movement (with wall sliding), ground following and
// jump physics. dt is in 60 Hz frame units (matching the original time scaling).
func (c *Camera) Update(m Move, dt float64) {
	sy, cy := math.Sin(c.Yaw), math.Cos(c.Yaw)
	spd := c.cfg.WalkSpeed * dt
	var dx, dz float64
	if m.Forward {
		dx -= sy * spd
		dz -= cy * spd
	}
	if m.Back {
		dx += sy * spd
		dz += cy * spd
	}
	if m.Left {
		dx -= cy * spd
		dz += sy * spd
	}
	if m.Right {
		dx += cy * spd
		dz -= sy * spd
	}

	// Horizontal move, resolved per-axis so the player slides along walls.
	if c.world != nil && !c.NoClip {
		feetY := c.Pos.Y - c.cfg.EyeHeight
		headY := c.Pos.Y
		r := c.cfg.CollisionRadius
		step := c.cfg.StepHeight
		if dx != 0 && !c.world.Blocked(c.Pos.X+dx, c.Pos.Z, feetY, headY, r, step) {
			c.Pos.X += dx
		}
		if dz != 0 && !c.world.Blocked(c.Pos.X, c.Pos.Z+dz, feetY, headY, r, step) {
			c.Pos.Z += dz
		}
	} else {
		c.Pos.X += dx
		c.Pos.Z += dz
	}

	// Ground following + jump physics.
	standEye := c.cfg.EyeHeight
	if c.world != nil {
		standEye = c.world.GroundHeight(c.Pos.X, c.Pos.Z, c.Pos.Y) + c.cfg.EyeHeight
	}
	if m.Jump && c.onGround {
		c.velY = c.cfg.JumpVelocity
		c.onGround = false
	}
	c.velY -= c.cfg.Gravity * dt
	c.Pos.Y += c.velY * dt
	if c.Pos.Y <= standEye {
		c.Pos.Y = standEye
		c.velY = 0
		c.onGround = true
	} else {
		c.onGround = false
	}
}

// Look applies a relative mouse motion (in pixels) to yaw and pitch.
// Moving the mouse right turns the view right (non-inverted X).
func (c *Camera) Look(dx, dy float64) {
	c.Yaw -= dx * lookGain
	c.Pitch = math.Max(-maxPitch, math.Min(maxPitch, c.Pitch-dy*lookGain))
}

// Basis returns the orthonormal camera frame (forward, right, up).
func (c *Camera) Basis() (fwd, right, up vec.V) {
	sy, cy := math.Sin(c.Yaw), math.Cos(c.Yaw)
	cp := math.Cos(c.Pitch)
	fwd = vec.V{X: -sy * cp, Y: math.Sin(c.Pitch), Z: -cy * cp}
	right = vec.V{X: cy, Y: 0, Z: -sy}
	up = fwd.Cross(right).Neg()
	return
}

// Ray builds a normalized primary ray through normalized screen coords
// u in [-1,1] (right) and v in [-1,1] (up).
func (c *Camera) Ray(fwd, right, up vec.V, u, v, aspect, fovScale float64) vec.Ray {
	dir := fwd.
		Add(right.Scale(u * aspect * fovScale)).
		Add(up.Scale(v * fovScale)).
		Normalize()
	return vec.Ray{Origin: c.Pos, Dir: dir}
}
