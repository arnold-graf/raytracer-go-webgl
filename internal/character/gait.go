package character

import (
	"math"

	"raytracer/internal/vec"
)

// GaitState is the locomotion mode derived from agent speed.
type GaitState int

const (
	GaitIdle GaitState = iota
	GaitWalk
	GaitRun
)

// GaitStateForSpeed maps speed (m/s) to idle/walk/run.
func GaitStateForSpeed(speed float64) GaitState {
	if speed < 0.05 {
		return GaitIdle
	}
	if speed < 3.5 {
		return GaitWalk
	}
	return GaitRun
}

// Foot tracks one foot's planted/swinging state.
type Foot struct {
	World       vec.V
	Initialized bool
	Stepping    bool
	StepFrom    vec.V
	StepTo      vec.V
	StepT       float64
}

// Locomotor drives procedural walk on uneven ground.
type Locomotor struct {
	HipPos  vec.V
	Heading float64
	Speed   float64
	Phase   float64
	Left    Foot
	Right   Foot
}

// NewLocomotor creates locomotion state grounded at spawn.
func NewLocomotor(rig *Rig, spawn vec.V, heading, speed float64, world FootWorld) Locomotor {
	headY := spawn.Y + rig.HipHeight + 0.5
	gy := spawn.Y
	if world != nil {
		gy = world.GroundHeight(spawn.X, spawn.Z, headY)
	}
	hip := HipPositionFromGround(spawn.X, gy, spawn.Z, rig.HipHeight)
	loc := Locomotor{
		HipPos:  hip,
		Heading: heading,
		Speed:   speed,
	}
	if world != nil {
		loc.initFeet(rig, world)
	}
	return loc
}

func (loc *Locomotor) initFeet(rig *Rig, world FootWorld) {
	gait := rig.GaitForSpeed(loc.Speed)
	hipBase := loc.hipBaseY(rig, world)
	// sideSign matches thigh_l (+X) and thigh_r (-X) hip sockets in the rig.
	loc.updateFoot(&loc.Left, 1, 0, gait, rig, world, hipBase, 0)
	loc.updateFoot(&loc.Right, -1, 0.5, gait, rig, world, hipBase, 0)
}

// Update advances hip motion, foot placement, and step arcs.
func (loc *Locomotor) Update(dt float64, rig *Rig, world FootWorld) {
	if loc.Speed < 0.05 || world == nil {
		return
	}
	gait := rig.GaitForSpeed(loc.Speed)
	loc.Phase += dt * gait.StepRate
	if loc.Phase > 1e6 {
		loc.Phase -= math.Floor(loc.Phase)
	}

	fwd := yawForward(loc.Heading)
	loc.HipPos = loc.HipPos.Add(fwd.Scale(loc.Speed * dt))

	hipBase := loc.hipBaseY(rig, world)
	loc.HipPos.Y = hipBase + math.Sin(loc.Phase*2*math.Pi)*gait.Bob

	loc.updateFoot(&loc.Left, 1, 0, gait, rig, world, hipBase, dt)
	loc.updateFoot(&loc.Right, -1, 0.5, gait, rig, world, hipBase, dt)
}

func (loc *Locomotor) hipBaseY(rig *Rig, world FootWorld) float64 {
	headY := loc.HipPos.Y + rig.HipHeight + 0.5
	gy := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, headY)
	return gy + rig.HipHeight
}

func (loc *Locomotor) updateFoot(foot *Foot, sideSign float64, phaseOff float64, gait GaitParams, rig *Rig, world FootWorld, hipBaseY float64, dt float64) {
	fwd, right := yawForward(loc.Heading), yawRight(loc.Heading)
	lateral := right.Scale(sideSign * 0.14)

	phase := loc.Phase + phaseOff
	fwdOff := math.Cos(phase*2*math.Pi) * gait.Stride * 0.5
	hipBase := vec.V{X: loc.HipPos.X, Y: hipBaseY, Z: loc.HipPos.Z}
	target := hipBase.Add(fwd.Scale(fwdOff)).Add(lateral)
	headY := hipBaseY + 0.5
	target.Y = world.GroundHeight(target.X, target.Z, headY)

	if !foot.Initialized {
		foot.World = target
		foot.Initialized = true
		return
	}

	if foot.Stepping {
		stepDur := 0.5 / gait.StepRate
		if stepDur < 1e-6 {
			stepDur = 0.15
		}
		foot.StepT += dt / stepDur
		if foot.StepT >= 1 {
			foot.Stepping = false
			foot.World = foot.StepTo
		} else {
			foot.World = footArc(foot.StepFrom, foot.StepTo, gait.Lift, foot.StepT)
		}
		return
	}

	if horizDist(foot.World, target) > gait.Stride*0.35 {
		foot.Stepping = true
		foot.StepFrom = foot.World
		foot.StepTo = target
		foot.StepT = 0
		return
	}
	foot.World = target
}

func footArc(from, to vec.V, lift, t float64) vec.V {
	mid := from.Add(to).Scale(0.5)
	mid.Y += lift
	u := 1 - t
	return from.Scale(u * u).Add(mid.Scale(2 * u * t)).Add(to.Scale(t * t))
}

func horizDist(a, b vec.V) float64 {
	dx, dz := a.X-b.X, a.Z-b.Z
	return math.Hypot(dx, dz)
}

func yawForward(yawDeg float64) vec.V {
	rad := yawDeg * math.Pi / 180
	return vec.V{X: -math.Sin(rad), Z: -math.Cos(rad)}
}

func yawRight(yawDeg float64) vec.V {
	rad := yawDeg * math.Pi / 180
	return vec.V{X: math.Cos(rad), Z: -math.Sin(rad)}
}
