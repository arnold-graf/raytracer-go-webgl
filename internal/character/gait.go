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

// Foot tracks one foot through explicit swing/stance phases.
type Foot struct {
	World        vec.V
	PlantWorld   vec.V
	SwingTo      vec.V // frozen landing target for the current swing arc
	PlantGroundY float64 // locked ground height at landing (Y stays fixed during stance)
	PlantNormal  vec.V
	Initialized  bool
	Phase        FootPhase
	StanceT      float64 // 0..1 within stance segment
	SwingT       float64 // 0..1 within swing segment
}

// Locomotor drives procedural walk on uneven ground.
type Locomotor struct {
	HipPos  vec.V
	GroundY float64 // smoothed ground height from planted feet
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
		GroundY: gy,
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
	hipBase := loc.GroundY + rig.HipHeight
	loc.updateFoot(&loc.Left, 1, 0, gait, world, hipBase)
	loc.updateFoot(&loc.Right, -1, 0.5, gait, world, hipBase)
}

// hipGroundSmoothRate controls pelvis descent; climbing uses hipGroundRiseRate.
const (
	hipGroundSmoothRate = 5.0
	hipGroundRiseRate   = 14.0
	groundHeadClearance = 2.5 // headY hint so GroundHeight sees steps above the pelvis
)

// Update advances hip motion, foot placement, and step arcs.
func (loc *Locomotor) Update(dt float64, rig *Rig, world FootWorld) {
	if loc.Speed < 0.05 || world == nil {
		return
	}
	gait := rig.GaitForSpeed(loc.Speed)
	travel := gait.TravelSpeed(loc.Speed)
	loc.Phase += dt * gait.StepRate
	if loc.Phase > 1e6 {
		loc.Phase -= math.Floor(loc.Phase)
	}

	fwd := yawForward(loc.Heading)
	loc.HipPos = loc.HipPos.Add(fwd.Scale(travel * dt))

	// Preliminary hip base for ground queries this tick.
	prelimBase := loc.GroundY + rig.HipHeight
	targetGy := loc.targetGroundY(rig, gait, world, prelimBase)
	rate := hipGroundSmoothRate
	if targetGy > loc.GroundY {
		rate = hipGroundRiseRate
	}
	blend := rate * dt
	if blend > 1 {
		blend = 1
	}
	loc.GroundY += (targetGy - loc.GroundY) * blend
	hipBase := loc.GroundY + rig.HipHeight
	loc.HipPos.Y = hipBase + math.Sin(loc.Phase*2*math.Pi)*gait.Bob

	loc.updateFoot(&loc.Left, 1, 0, gait, world, hipBase)
	loc.updateFoot(&loc.Right, -1, 0.5, gait, world, hipBase)
}

func groundHeadHint(loc *Locomotor, hipBaseY float64) float64 {
	h := loc.HipPos.Y + groundHeadClearance
	if hipBaseY+0.5 > h {
		h = hipBaseY + 0.5
	}
	return h
}

func (loc *Locomotor) targetGroundY(rig *Rig, gait GaitParams, world FootWorld, hipBaseY float64) float64 {
	y := loc.plantedGroundY()
	headY := groundHeadHint(loc, hipBaseY)

	if gy := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, headY); gy > y {
		y = gy
	}

	for _, spec := range []struct {
		foot     *Foot
		sideSign float64
		off      float64
	}{
		{&loc.Left, 1, 0},
		{&loc.Right, -1, 0.5},
	} {
		f := spec.foot
		if !f.Initialized || f.Phase != FootSwing || f.SwingT < 0.45 {
			continue
		}
		tgt := loc.footTarget(spec.sideSign, spec.off, gait, world, hipBaseY)
		if tgt.Y > y {
			y = tgt.Y
		}
	}
	return y
}

func (loc *Locomotor) plantedGroundY() float64 {
	var ys []float64
	for _, f := range []Foot{loc.Left, loc.Right} {
		if f.Initialized && f.Phase != FootSwing {
			ys = append(ys, f.PlantGroundY)
		}
	}
	if len(ys) == 0 {
		return loc.GroundY
	}
	y := ys[0]
	for _, v := range ys[1:] {
		if v > y {
			y = v
		}
	}
	return y
}

func (loc *Locomotor) footTarget(sideSign, phaseOff float64, gait GaitParams, world FootWorld, hipBaseY float64) vec.V {
	return loc.footTargetAt(loc.Phase, loc.HipPos, sideSign, phaseOff, gait, world, hipBaseY)
}

func (loc *Locomotor) footTargetAt(phase float64, hip vec.V, sideSign, phaseOff float64, gait GaitParams, world FootWorld, hipBaseY float64) vec.V {
	fwd, right := yawForward(loc.Heading), yawRight(loc.Heading)
	lateral := right.Scale(sideSign * 0.14)
	travel := gait.TravelSpeed(loc.Speed)
	stride := gait.StepStride(travel)
	// Negative cos: foot lands ahead of the hip in the travel direction at heel strike.
	fwdOff := -math.Cos((phase+phaseOff)*2*math.Pi) * stride * 0.5
	hipBase := vec.V{X: hip.X, Y: hipBaseY, Z: hip.Z}
	target := hipBase.Add(fwd.Scale(fwdOff)).Add(lateral)
	target.Y = world.GroundHeight(target.X, target.Z, groundHeadHint(loc, hipBaseY))
	return target
}

func lockFootPlant(foot *Foot, world FootWorld) {
	n := world.GroundNormal(foot.PlantWorld.X, foot.PlantWorld.Z, foot.PlantWorld.Y+0.5)
	if n.LenSq() < 1e-12 {
		n = vec.V{Y: 1}
	} else {
		n = n.Normalize()
	}
	foot.PlantNormal = n
}

func (loc *Locomotor) updateFoot(foot *Foot, sideSign, phaseOff float64, gait GaitParams, world FootWorld, hipBaseY float64) {
	target := loc.footTarget(sideSign, phaseOff, gait, world, hipBaseY)

	footCycle := loc.Phase + phaseOff
	footCycle -= math.Floor(footCycle)

	if !foot.Initialized {
		foot.PlantWorld = target
		foot.PlantGroundY = target.Y
		foot.World = target
		lockFootPlant(foot, world)
		foot.Phase = FootMidStance
		foot.StanceT = 0.5
		foot.Initialized = true
		return
	}

	wasSwing := foot.Phase == FootSwing

	if footCycle < swingFraction {
		if !wasSwing {
			// Freeze landing at end-of-swing using hip position when the foot lands.
			phaseDelta := swingFraction - footCycle
			landPhase := loc.Phase + phaseDelta
			travel := gait.TravelSpeed(loc.Speed)
			fwd := yawForward(loc.Heading)
			hipAtLand := loc.HipPos.Add(fwd.Scale(travel * phaseDelta / gait.StepRate))
			foot.SwingTo = loc.footTargetAt(landPhase, hipAtLand, sideSign, phaseOff, gait, world, hipBaseY)
		}
		foot.Phase = FootSwing
		foot.SwingT = footCycle / swingFraction
		foot.StanceT = 0
		from := foot.PlantWorld
		foot.World = footArc(from, foot.SwingTo, gait.Lift, foot.SwingT)
		return
	}

	foot.StanceT = (footCycle - swingFraction) / (1 - swingFraction)
	foot.SwingT = 0
	foot.Phase = stanceSubPhase(foot.StanceT)
	if wasSwing {
		foot.PlantWorld = foot.SwingTo
		foot.PlantGroundY = foot.PlantWorld.Y
		lockFootPlant(foot, world)
	}
	foot.World = foot.PlantWorld
}

func footArc(from, to vec.V, lift, t float64) vec.V {
	mid := from.Add(to).Scale(0.5)
	mid.Y += lift
	u := 1 - t
	return from.Scale(u * u).Add(mid.Scale(2 * u * t)).Add(to.Scale(t * t))
}

func yawForward(yawDeg float64) vec.V {
	rad := yawDeg * math.Pi / 180
	return vec.V{X: -math.Sin(rad), Z: -math.Cos(rad)}
}

func yawRight(yawDeg float64) vec.V {
	rad := yawDeg * math.Pi / 180
	return vec.V{X: math.Cos(rad), Z: -math.Sin(rad)}
}
