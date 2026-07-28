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

// Foot tracks one foot through explicit swing/stance phases.
type Foot struct {
	World        vec.V
	PlantWorld   vec.V
	SwingFrom    vec.V
	SwingTo      vec.V // frozen landing target for the current swing arc
	PlantGroundY float64 // locked ground height at landing (Y stays fixed during stance)
	PlantNormal  vec.V
	Initialized  bool
	Phase        FootPhase
	StanceT      float64 // 0..1 within stance segment
	SwingT       float64 // 0..1 within swing segment
	Solve        LegSolve // cached IK joints for temporal continuity
}

// Locomotor drives procedural walk on uneven ground.
type Locomotor struct {
	HipPos     vec.V
	BodyPitch  float64 // degrees, multiped balance
	BodyRoll   float64
	GroundY    float64 // smoothed ground height from planted feet
	Heading    float64
	Speed      float64
	Phase      float64
	Left       Foot // synced from Feet[0] on bipeds
	Right      Foot // synced from Feet[1] on bipeds
	Feet       []Foot
}

func (loc *Locomotor) ensureFeet(n int) {
	if n <= 0 {
		n = 2
	}
	if len(loc.Feet) != n {
		loc.Feet = make([]Foot, n)
	}
}

func (loc *Locomotor) syncBipedFeet() {
	if len(loc.Feet) >= 2 {
		loc.Left = loc.Feet[0]
		loc.Right = loc.Feet[1]
	}
}

func (loc *Locomotor) plantedFeet() []Foot {
	if len(loc.Feet) > 0 {
		out := make([]Foot, len(loc.Feet))
		copy(out, loc.Feet)
		return out
	}
	return []Foot{loc.Left, loc.Right}
}

func (loc *Locomotor) footSlice() []*Foot {
	if len(loc.Feet) > 0 {
		out := make([]*Foot, len(loc.Feet))
		for i := range loc.Feet {
			out[i] = &loc.Feet[i]
		}
		return out
	}
	return []*Foot{&loc.Left, &loc.Right}
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
	loc.syncBipedFeet()
	return loc
}

func (loc *Locomotor) initFeet(rig *Rig, world FootWorld) {
	legs := rig.LegDefs()
	loc.ensureFeet(len(legs))
	gait := rig.GaitForSpeed(loc.Speed)
	hipBase := loc.GroundY + rig.HipHeight
	for i, leg := range legs {
		loc.updateFoot(rig, &loc.Feet[i], leg, gait, world, hipBase)
	}
	loc.syncBipedFeet()
}

// hipGroundSmoothRate controls pelvis descent; climbing uses hipGroundRiseRate.
const (
	hipGroundSmoothRate        = 5.0
	hipGroundRiseRate          = 10.0
	hipPlantRisePerSec         = 2.5  // smooth catch-up when a foot lands on a higher tread
	swingHipPreviewStart       = 0.65 // blend hips toward landing over the last 35% of flat swing
	swingHipPreviewStartStepUp = 0.68 // step-up swings: pelvis follows the foot slightly later
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
	targetGy := loc.targetGroundY(rig, world, prelimBase)
	rate := hipGroundSmoothRate
	if targetGy > loc.GroundY {
		rate = hipGroundRiseRate
	}
	blend := rate * dt
	if blend > 1 {
		blend = 1
	}
	loc.GroundY += (targetGy - loc.GroundY) * blend
	loc.updateHipHeight(rig, gait)

	hipBase := loc.GroundY + rig.HipHeight
	legs := rig.LegDefs()
	loc.ensureFeet(len(legs))
	for i, leg := range legs {
		loc.updateFoot(rig, &loc.Feet[i], leg, gait, world, hipBase)
	}
	loc.enforceFootSeparation(rig)
	loc.catchUpGroundFromPlantedFeet(rig, gait, dt)
	loc.updateBodyBalance(rig, dt)
	loc.syncBipedFeet()
}

func groundHeadHint(loc *Locomotor, rig *Rig, hipBaseY float64) float64 {
	h := loc.HipPos.Y + rig.Locomotion.HeadClearance
	if hipBaseY+0.5 > h {
		h = hipBaseY + 0.5
	}
	return h
}

func footStepUp(from, to vec.V) float64 {
	return to.Y - from.Y
}

// stepUpIntensity scales step-up effects by riser height; first tread from flat ground is softer.
func stepUpIntensity(stepUp, fromGroundY float64, loc LocomotionParams) float64 {
	if stepUp <= loc.StepUpMinHeight {
		return 0
	}
	height := (stepUp - loc.StepUpMinHeight) / (0.40 - loc.StepUpMinHeight)
	scale := 0.62 + 0.38*clamp(height, 0, 1)
	if fromGroundY < 0.12 {
		scale *= 0.80
	}
	return scale
}

func (loc *Locomotor) targetGroundY(rig *Rig, world FootWorld, hipBaseY float64) float64 {
	y := loc.plantedGroundY()
	stepUpSwing := loc.stepUpSwingProgress(rig.Locomotion)

	// Late swing preview: on step-ups, delay pelvis rise so the climbing foot bends the knee first.
	for _, f := range loc.plantedFeet() {
		if !f.Initialized || f.Phase != FootSwing {
			continue
		}
		stepUp := footStepUp(f.PlantWorld, f.SwingTo)
		previewStart := swingHipPreviewStart
		if stepUp > rig.Locomotion.StepUpMinHeight {
			intensity := stepUpIntensity(stepUp, f.PlantGroundY, rig.Locomotion)
			previewStart += (swingHipPreviewStartStepUp - swingHipPreviewStart) * intensity
		}
		if f.SwingT < previewStart {
			continue
		}
		blend := (f.SwingT - previewStart) / (1.0 - previewStart)
		if blend > 1 {
			blend = 1
		}
		preview := y + (f.SwingTo.Y-y)*blend
		if preview > y {
			y = preview
		}
	}

	// Terrain under the pelvis: on flat/downhill use forward probe. During an active step-up
	// swing, only allow gradual rise with swing progress so the torso does not outrun the foot.
	if world != nil {
		terrainGy := loc.pelvisTerrainY(rig, world, hipBaseY)
		if terrainGy > y {
			if stepUpSwing.active {
				intensity := stepUpIntensity(stepUpSwing.stepUp, loc.plantedGroundY(), rig.Locomotion)
				cap := y + stepUpSwing.stepUp*stepUpSwing.progress*(0.55+0.25*intensity)
				if terrainGy > cap {
					terrainGy = cap
				}
			}
			if terrainGy > y {
				y = terrainGy
			}
		}
	}
	return y
}

type stepUpSwingState struct {
	active   bool
	stepUp   float64
	progress float64 // max SwingT among active step-up swings
}

func (loc *Locomotor) stepUpSwingProgress(locParams LocomotionParams) stepUpSwingState {
	var st stepUpSwingState
	for _, f := range loc.plantedFeet() {
		if !f.Initialized || f.Phase != FootSwing {
			continue
		}
		stepUp := footStepUp(f.PlantWorld, f.SwingTo)
		if stepUp <= locParams.StepUpMinHeight {
			continue
		}
		st.active = true
		if stepUp > st.stepUp {
			st.stepUp = stepUp
		}
		if f.SwingT > st.progress {
			st.progress = f.SwingT
		}
	}
	return st
}

func (loc *Locomotor) updateHipHeight(rig *Rig, gait GaitParams) {
	hipBase := loc.GroundY + rig.HipHeight
	loc.HipPos.Y = hipBase + math.Sin(loc.Phase*2*math.Pi)*gait.Bob
}

func (loc *Locomotor) pelvisTerrainY(rig *Rig, world FootWorld, hipBaseY float64) float64 {
	headY := groundHeadHint(loc, rig, hipBaseY)
	center := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, headY)
	fwd := yawForward(loc.Heading)
	front := world.GroundHeight(loc.HipPos.X+fwd.X*rig.Locomotion.PelvisForwardProbe, loc.HipPos.Z+fwd.Z*rig.Locomotion.PelvisForwardProbe, headY)
	if front > center {
		return front
	}
	return center
}

// catchUpGroundFromPlantedFeet eases pelvis rise when a foot lands on a higher tread.
func (loc *Locomotor) catchUpGroundFromPlantedFeet(rig *Rig, gait GaitParams, dt float64) {
	planted := loc.plantedGroundY()
	if planted <= loc.GroundY {
		return
	}
	maxRise := hipPlantRisePerSec * dt
	rise := planted - loc.GroundY
	if rise > maxRise {
		rise = maxRise
	}
	loc.GroundY += rise
	loc.updateHipHeight(rig, gait)
}

func (loc *Locomotor) plantedGroundY() float64 {
	var ys []float64
	for _, f := range loc.plantedFeet() {
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

func (loc *Locomotor) lowerPlantedGroundY() float64 {
	var ys []float64
	for _, f := range loc.plantedFeet() {
		if f.Initialized && f.Phase != FootSwing {
			ys = append(ys, f.PlantGroundY)
		}
	}
	if len(ys) == 0 {
		return loc.GroundY
	}
	y := ys[0]
	for _, v := range ys[1:] {
		if v < y {
			y = v
		}
	}
	return y
}

func (loc *Locomotor) footTarget(rig *Rig, leg LegDef, gait GaitParams, world FootWorld, hipBaseY float64) vec.V {
	return loc.planLegTarget(rig, leg, loc.Phase, leg.PhaseOffset, gait, world, hipBaseY, loc.HipPos)
}

func (loc *Locomotor) footTargetAt(rig *Rig, leg LegDef, phase float64, hip vec.V, gait GaitParams, world FootWorld, hipBaseY float64) vec.V {
	return loc.planLegTarget(rig, leg, phase, leg.PhaseOffset, gait, world, hipBaseY, hip)
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

func (loc *Locomotor) updateFoot(rig *Rig, foot *Foot, leg LegDef, gait GaitParams, world FootWorld, hipBaseY float64) {
	target := loc.footTarget(rig, leg, gait, world, hipBaseY)
	phaseOff := leg.PhaseOffset

	footCycle := loc.Phase + phaseOff
	footCycle -= math.Floor(footCycle)
	swingFraction := rig.Locomotion.SwingFraction

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
			phaseDelta := swingFraction - footCycle
			landPhase := loc.Phase + phaseDelta
			travel := gait.TravelSpeed(loc.Speed)
			fwd := yawForward(loc.Heading)
			hipAtLand := loc.HipPos.Add(fwd.Scale(travel * phaseDelta / gait.StepRate))
			foot.SwingTo = loc.footTargetAt(rig, leg, landPhase, hipAtLand, gait, world, hipBaseY)
		}
		foot.Phase = FootSwing
		foot.SwingT = footCycle / swingFraction
		foot.StanceT = 0
		from := foot.PlantWorld
		rightVec := yawRight(loc.Heading)
		if leg.Kind == LegKindTripod {
			rightVec := yawRight(loc.Heading)
			foot.World = footSwingArcMultiped(from, foot.SwingTo, gait.Lift, foot.SwingT, loc.HipPos, rightVec, leg, rig.Locomotion)
		} else {
			foot.World = footSwingArc(from, foot.SwingTo, gait.Lift, foot.SwingT, loc.HipPos, rightVec, leg.SideSign, rig.Locomotion)
		}
		return
	}

	foot.StanceT = (footCycle - swingFraction) / (1 - swingFraction)
	foot.SwingT = 0
	foot.Phase = stanceSubPhase(foot.StanceT, rig.Locomotion.FootRoll)
	if wasSwing {
		foot.PlantWorld = foot.SwingTo
		foot.PlantGroundY = foot.PlantWorld.Y
		lockFootPlant(foot, world)
	}
	foot.World = foot.PlantWorld
}

func yawForward(yawDeg float64) vec.V {
	rad := yawDeg * math.Pi / 180
	return vec.V{X: -math.Sin(rad), Z: -math.Cos(rad)}
}

func yawRight(yawDeg float64) vec.V {
	rad := yawDeg * math.Pi / 180
	return vec.V{X: math.Cos(rad), Z: -math.Sin(rad)}
}
