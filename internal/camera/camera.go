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

	// SprintMultiplier scales WalkSpeed while sprinting (Shift). CrouchEyeHeight
	// is the camera height when fully crouched (lower than EyeHeight), and
	// CrouchSpeedMultiplier scales WalkSpeed while crouched. Sprint and crouch
	// blend in/out smoothly (see stanceEase), so the eye height and speed glide
	// between stances rather than snapping.
	SprintMultiplier      float64
	CrouchEyeHeight       float64
	CrouchSpeedMultiplier float64

	// DoubleJumpEnabled allows one extra jump while airborne (requires releasing
	// and pressing jump again mid-air).
	DoubleJumpEnabled bool
}

// DefaultConfig returns the built-in movement tuning.
func DefaultConfig() Config {
	return Config{
		WalkSpeed:             0.35,
		EyeHeight:             1.6,
		JumpVelocity:          0.42,
		Gravity:               0.20,
		CollisionRadius:       0.30,
		StepHeight:            0.45,
		SprintMultiplier:      2.0,
		CrouchEyeHeight:       0.8,
		CrouchSpeedMultiplier: 0.5,
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
	airJumps int  // jumps used since last landing (0, 1, or 2)
	jumpPrev bool // previous frame jump held (edge-detect air jumps)

	// crouchT and sprintT are the eased stance blends in [0,1] (0 = standing /
	// not sprinting, 1 = fully crouched / sprinting). They follow the held keys
	// over a few frames so the eye height and walk speed transition smoothly.
	crouchT float64
	sprintT float64
}

// stanceEase is the per-frame blend factor toward the target crouch/sprint
// state. ~0.2 reaches the new stance in a handful of 60 Hz frames (~0.15 s):
// smooth, but responsive enough to feel immediate.
const stanceEase = 0.2

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// New returns a camera at the original starting position, facing into the scene.
func New() *Camera {
	return &Camera{Pos: vec.New(0, 1.6, 5), Yaw: 0, cfg: DefaultConfig()}
}

// Pose is a saved camera position and orientation.
type Pose struct {
	Pos   vec.V
	Yaw   float64
	Pitch float64
}

// Pose returns a copy of the current camera pose.
func (c *Camera) Pose() Pose {
	return Pose{Pos: c.Pos, Yaw: c.Yaw, Pitch: c.Pitch}
}

// SetPose restores position and orientation (does not reset velocity).
func (c *Camera) SetPose(p Pose) {
	c.Pos, c.Yaw, c.Pitch = p.Pos, p.Yaw, p.Pitch
}

// SetConfig replaces the movement tuning.
func (c *Camera) SetConfig(cfg Config) { c.cfg = cfg }

// OnGround reports whether the player is currently standing on a surface (as
// opposed to airborne mid-jump/fall). Used to gate footstep sounds.
func (c *Camera) OnGround() bool { return c.onGround }

// EyeHeight returns the current camera height above the surface underfoot,
// interpolated toward the crouch height by the active crouch blend.
func (c *Camera) EyeHeight() float64 {
	return lerp(c.cfg.EyeHeight, c.cfg.CrouchEyeHeight, c.crouchT)
}

// SetWorld attaches the collision/terrain world (nil = flat plane at y=0).
func (c *Camera) SetWorld(w World) { c.world = w }

// SnapToGround places the camera directly on the surface underfoot, avoiding an
// initial fall when a scene's start pose is above the ground. headY is the
// camera eye height so ceilings above the player are ignored.
func (c *Camera) SnapToGround() {
	if c.world == nil {
		return
	}
	g := c.world.GroundHeight(c.Pos.X, c.Pos.Z, c.Pos.Y)
	c.Pos.Y = g + c.cfg.EyeHeight
	c.velY = 0
	c.onGround = true
}

// PlaceOnFloor sets the eye height above an explicit floor Y and clears vertical
// velocity. Use when the spawn surface is known (e.g. portal into the Cube) so
// a broad SnapToGround query does not land on geometry overhead.
func (c *Camera) PlaceOnFloor(floorY float64) {
	c.Pos.Y = floorY + c.EyeHeight()
	c.velY = 0
	c.onGround = true
}

// Land clears vertical motion and marks the camera grounded at the current height.
func (c *Camera) Land() {
	c.velY = 0
	c.onGround = true
}

// Move describes which movement inputs are active this frame. It carries both
// the digital (keyboard) flags and the analog (gamepad) axes; the caller fills
// whichever it has and Update combines them.
type Move struct {
	Forward, Back, Left, Right, Jump bool
	// Sprint (Shift) speeds movement up; Crouch (C) lowers the camera and slows
	// movement. Both ease in/out rather than toggling instantly.
	Sprint, Crouch bool

	// MoveX/MoveZ are analog, body-relative stick input added on top of the
	// digital flags: +X strafes right, +Z walks forward, and the magnitude
	// (0..1) scales speed for fine analog control. SprintAxis/CrouchAxis are
	// analog trigger pulls (0..1) layered onto the Sprint/Crouch flags.
	MoveX, MoveZ           float64
	SprintAxis, CrouchAxis float64
}

// boolMax returns the larger of a boolean (as 0/1) and an analog value, used to
// merge a digital key with an analog axis for the same action.
func boolMax(b bool, v float64) float64 {
	if b && v < 1 {
		return 1
	}
	return v
}

// Update applies horizontal movement (with wall sliding), ground following and
// jump physics. dt is in 60 Hz frame units (matching the original time scaling).
func (c *Camera) Update(m Move, dt float64) {
	// Resolve sprint/crouch from the digital flags and analog trigger pulls
	// (0..1). Crouch wins over sprint, so crouching while sprinting fades the
	// boost out smoothly. Both targets then ease in/out across frames.
	crouchInput := boolMax(m.Crouch, m.CrouchAxis)
	sprintInput := boolMax(m.Sprint, m.SprintAxis)
	crouchTarget := crouchInput
	sprintTarget := sprintInput * (1 - crouchInput)
	c.crouchT += (crouchTarget - c.crouchT) * stanceEase
	c.sprintT += (sprintTarget - c.sprintT) * stanceEase

	eye := lerp(c.cfg.EyeHeight, c.cfg.CrouchEyeHeight, c.crouchT)
	speedMul := lerp(1, c.cfg.SprintMultiplier, c.sprintT) * lerp(1, c.cfg.CrouchSpeedMultiplier, c.crouchT)

	// Desired horizontal motion, body-relative: fwd>0 ahead, strafe>0 right.
	// Digital keys contribute ±1; analog stick adds its value. The magnitude is
	// clamped to 1 so diagonal keys and over-range sticks never exceed full
	// speed, but partial stick deflection still scales the speed down (analog).
	fwd, strafe := 0.0, 0.0
	if m.Forward {
		fwd += 1
	}
	if m.Back {
		fwd -= 1
	}
	if m.Right {
		strafe += 1
	}
	if m.Left {
		strafe -= 1
	}
	fwd += m.MoveZ
	strafe += m.MoveX
	if mag := math.Hypot(fwd, strafe); mag > 1 {
		fwd /= mag
		strafe /= mag
	}

	sy, cy := math.Sin(c.Yaw), math.Cos(c.Yaw)
	spd := c.cfg.WalkSpeed * speedMul * dt
	dx := (strafe*cy - fwd*sy) * spd
	dz := (-strafe*sy - fwd*cy) * spd

	// Horizontal move, resolved per-axis so the player slides along walls.
	if c.world != nil && !c.NoClip {
		feetY := c.Pos.Y - eye
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

	// Ground following + jump physics. The "stand" eye uses the eased crouch
	// height, so dropping into / rising out of a crouch glides the camera down
	// and back up instead of snapping.
	standEye := eye
	if c.world != nil {
		standEye = c.world.GroundHeight(c.Pos.X, c.Pos.Z, c.Pos.Y) + eye
	}
	if m.Jump {
		if c.onGround {
			c.velY = c.cfg.JumpVelocity
			c.onGround = false
			c.airJumps = 1
		} else if c.cfg.DoubleJumpEnabled && c.airJumps == 1 && !c.jumpPrev {
			c.velY = c.cfg.JumpVelocity
			c.airJumps = 2
		}
	}
	c.jumpPrev = m.Jump

	if c.onGround {
		// Stay glued to the ground while walking, following small rises and dips
		// in the terrain without ever going airborne. Without this, walking
		// downhill leaves the eye briefly above the lowered ground, registering a
		// one-frame "fall" and "landing" every frame — which the footstep system
		// hears as a rapid drumroll of landing thuds. Only a drop larger than a
		// single step (a real ledge) starts an actual fall.
		const stepDownLimit = 0.75
		if c.Pos.Y-standEye > stepDownLimit {
			c.onGround = false
			c.velY -= c.cfg.Gravity * dt
			c.Pos.Y += c.velY * dt
		} else {
			c.Pos.Y = standEye
			c.velY = 0
		}
	} else {
		c.velY -= c.cfg.Gravity * dt
		c.Pos.Y += c.velY * dt
		if c.Pos.Y <= standEye {
			c.Pos.Y = standEye
			c.velY = 0
			c.onGround = true
			c.airJumps = 0
		}
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
