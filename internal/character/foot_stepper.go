package character

import (
	"math"

	"raytracer/internal/vec"
)

const (
	spiderStepHeight         = 2.0
	spiderDefaultOvershoot   = 1.40
	spiderStepCooldown       = 0.05
	spiderStopSteppingStill  = 2.0
	spiderRayFrontalLength   = 0.55
	spiderRayDownHeight      = 0.35
	spiderRayDownDepth       = 1.2
	spiderRayInwardsEnd      = 0.65
	spiderCastRadius         = 0.04
)

// legStepper handles step desire, surface finding, and step animation
// for one leg (Unity IKStepper port).
type legStepper struct {
	chain          *legChain
	leg            LegDef
	asyncIdx       []int
	stepCooldown   float64
	timeSinceStep  float64
	timeStill      float64
	isStepping     bool
	stepT          float64
	stepDuration   float64
	stepFrom       footTarget
	stepTo         footTarget
	prediction     vec.V
	defaultLocal   vec.V
	minDistance    float64
	defaultOffsetL float64
	defaultOffsetH float64
	defaultOffsetS float64
}

func newLegStepper(chain *legChain, leg LegDef, async []int) legStepper {
	minDist := 0.2 * chain.chainLength()
	return legStepper{
		chain:        chain,
		leg:          leg,
		asyncIdx:     async,
		stepCooldown: spiderStepCooldown,
		timeSinceStep: spiderStepCooldown * 2,
		minDistance:  minDist,
		defaultOffsetL: 0,
		defaultOffsetH: 0,
		defaultOffsetS: 0,
	}
}

func (st *legStepper) stepCheck(moving bool, spider *SpiderLocomotor, rig *Rig, footIdx int) bool {
	if st.isStepping {
		return false
	}
	if !moving {
		st.timeStill += 1.0 / 60.0
		if st.timeStill > spiderStopSteppingStill {
			return false
		}
		return !st.chain.Target.Grounded
	}
	st.timeStill = 0
	if !st.chain.Target.Grounded {
		return true
	}
	if st.chain.Error > st.chain.Tolerance {
		return true
	}
	hip := st.chain.Hinges[0].Point
	plant := st.plantPos(spider, footIdx)
	if horizDist(vec.V{X: hip.X, Z: hip.Z}, vec.V{X: plant.X, Z: plant.Z}) > spiderMaxHipPlantHoriz*0.68 {
		return true
	}
	if hip.Sub(plant).Len() > spiderMaxHipPlantHoriz*0.38 {
		return true
	}
	if spider != nil && rig != nil && moving {
		stride := spider.gaitStride(rig)
		fwd := spider.surfaceForward()
		if fwd.LenSq() < 1e-12 {
			fwd = yawForward(spider.Heading)
		}
		plant := st.plantPos(spider, footIdx)
		ahead := (hip.X-plant.X)*fwd.X + (hip.Z-plant.Z)*fwd.Z
		pace := spider.paceScale(rig)
		trigger := stride * spiderStepTriggerFraction / pace
		if pace > 1 {
			trigger /= math.Sqrt(pace)
		}
		minTrigger := spiderMaxHipPlantHoriz * 0.28
		if trigger < minTrigger {
			trigger = minTrigger
		}
		dist := horizDist(vec.V{X: hip.X, Z: hip.Z}, vec.V{X: plant.X, Z: plant.Z})
		if ahead > trigger {
			dist = math.Max(dist, ahead)
		}
		if dist > trigger {
			return true
		}
	}
	if hip.Sub(plant).Len() < st.minDistance {
		return true
	}
	return false
}

func (st *legStepper) plantPos(spider *SpiderLocomotor, footIdx int) vec.V {
	if spider != nil && footIdx >= 0 && footIdx < len(spider.Feet) {
		f := &spider.Feet[footIdx]
		if f.Initialized {
			return f.PlantWorld
		}
	}
	return st.chain.Target.Position
}

func (st *legStepper) beginStep(stepTime float64, spider *SpiderLocomotor, rig *Rig, world FootWorld, footIdx int) {
	if st.isStepping {
		return
	}
	if spider == nil || rig == nil || footIdx < 0 || footIdx >= len(spider.Feet) {
		return
	}
	bodyXf := spider.rootTransform()
	headY := spider.Body.Pos.Y + spider.RestHeight + 0.5
	stride := spider.gaitStride(rig)
	desired := spider.stepTargetForLeg(footIdx, st.leg, bodyXf, rig, world, headY, stride)

	f := &spider.Feet[footIdx]
	stepFromPos := f.PlantWorld
	if f.Solve.Valid {
		stepFromPos = f.Solve.Foot
	}
	if !st.chain.Target.Grounded {
		stepFromPos = st.chain.Target.Position
	}
	st.isStepping = true
	st.stepT = 0
	st.stepDuration = stepTime
	if st.stepDuration < 0.06 {
		st.stepDuration = 0.06
	}
	st.stepFrom = footTarget{Position: stepFromPos, Normal: spider.Up, Grounded: true}
	st.stepTo = footTarget{Position: desired, Normal: spider.Up, Grounded: true}
	st.chain.setTarget(st.stepFrom)
}

func (st *legStepper) allowedToStep(steppers []legStepper) bool {
	if st.isStepping {
		return false
	}
	if !st.chain.Target.Grounded {
		return true
	}
	if st.timeSinceStep < st.stepCooldown {
		return false
	}
	for _, idx := range st.asyncIdx {
		if idx >= 0 && idx < len(steppers) && steppers[idx].isStepping {
			return false
		}
	}
	return true
}

func (st *legStepper) advanceStep(dt float64, spider *SpiderLocomotor, world FootWorld) {
	if !st.isStepping {
		return
	}
	st.stepT += dt / st.stepDuration
	if st.stepT >= 1 {
		st.isStepping = false
		st.timeSinceStep = 0
		plant := st.stepTo.Position
		headY := spider.Body.Pos.Y + spider.RestHeight + 0.5
		if world != nil {
			plant.Y = world.GroundHeight(plant.X, plant.Z, headY)
		} else if plant.Y < spider.GroundY {
			plant.Y = spider.GroundY
		}
		st.stepTo.Position = plant
		st.chain.setTarget(st.stepTo)
		return
	}
	t := st.stepT
	up := spider.Up
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	}
	var pos vec.V
	if spider.rig != nil {
		lift := spider.rig.GaitForSpeed(spider.Speed).Lift
		if lift <= 0 {
			lift = 0.35
		}
		right := yawRight(bodyYawDeg(spider.rootTransform()))
		pos = footSwingArcMultiped(st.stepFrom.Position, st.stepTo.Position, lift, t, spider.Body.Pos, right, st.leg, spider.rig.Locomotion)
	} else {
		pos = st.stepFrom.Position.Scale(1 - t).Add(st.stepTo.Position.Scale(t))
		arc := spiderStepHeight * 0.01 * spiderScale * easeStepArc(t)
		pos = pos.Add(up.Scale(arc))
	}
	norm := st.stepFrom.Normal.Scale(1 - t).Add(st.stepTo.Normal.Scale(t))
	if norm.LenSq() > 1e-12 {
		norm = norm.Normalize()
	}
	bodyHip := spiderBodyHip(spider.rootTransform())
	pos = clampFootToLegSector(pos, bodyHip, st.leg, spider.Heading)
	headY := spider.Body.Pos.Y + spider.RestHeight + 0.5
	if world != nil {
		gy := world.GroundHeight(pos.X, pos.Z, headY)
		if pos.Y < gy {
			pos.Y = gy
		}
	} else if pos.Y < spider.GroundY {
		pos.Y = spider.GroundY
	}
	st.chain.setTarget(footTarget{Position: pos, Normal: norm, Grounded: false})
}

func (st *legStepper) calculateDesiredPosition(spider *SpiderLocomotor, world FootWorld) vec.V {
	ee := st.chain.tip()
	def := st.defaultPosition(spider, world)
	up := spider.Up
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	}
	start := projectOnPlane(ee, up)
	start.Y = def.Y
	overshoot := start.Add(def.Sub(start).Scale(spiderDefaultOvershoot))
	return overshoot
}

func (st *legStepper) defaultPosition(spider *SpiderLocomotor, world FootWorld) vec.V {
	bodyXf := spider.rootTransform()
	hip := bodyXf.ToWorld(spider.rig.JointLocal(st.leg.Proximal))
	chainLen := st.chain.chainLength()
	diameter := chainLen - st.minDistance
	heading := spider.Heading
	rest := hipWorldOffset(bodyXf.Translation(), hip.Y, heading, st.leg.RestOffset)
	radial := legRadialDir(bodyXf.Translation(), rest, heading)
	if radial.LenSq() < 1e-12 {
		radial = yawRight(heading)
	}
	def := hip.Add(radial.Scale(st.minDistance + 0.5*(1+st.defaultOffsetL)*diameter))
	def = def.Add(vec.V{Y: st.defaultOffsetH * 2 * spiderColliderRadius})
	stride := spider.gaitStride(spider.rig)
	fwd := spider.surfaceForward()
	lookahead := stride * spiderLookaheadFraction
	if spider.Speed > 1 {
		lookahead += (spider.Speed - 1) * 0.06
	}
	def.X += fwd.X * lookahead
	def.Z += fwd.Z * lookahead
	bodyHip := spiderBodyHip(bodyXf)
	def = clampFootToLegSector(def, bodyHip, st.leg, heading)
	def = clampFootToHipReach(def, hip, spiderMaxHipPlantHoriz*0.94)
	headY := spider.Body.Pos.Y + spider.RestHeight + 0.5
	if world != nil {
		def.Y = world.GroundHeight(def.X, def.Z, headY)
	}
	return def
}

func (st *legStepper) findTargetOnSurface(spider *SpiderLocomotor, rig *Rig, world FootWorld) footTarget {
	headY := spider.Body.Pos.Y + spider.RestHeight + 0.5
	def := st.defaultPosition(spider, world)
	pred := st.prediction
	if pred.LenSq() < 1e-12 {
		pred = def
	}
	up := spider.Up
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	}

	type cast struct {
		origin, end vec.V
		frontal     bool
	}
	var casts []cast

	frontal := spider.Body.Pos.Add(up.Scale(spiderColliderRadius * 0.5))
	planeDir := projectOnPlane(pred.Sub(frontal), up)
	var frontalEnd vec.V
	if planeDir.LenSq() > 1e-12 {
		frontalEnd = frontal.Add(planeDir.Normalize().Scale(spiderRayFrontalLength * st.chain.chainLength()))
	} else {
		fwd := spider.surfaceForward()
		if fwd.LenSq() < 1e-12 {
			fwd = yawForward(spider.Heading)
		}
		frontalEnd = frontal.Add(fwd.Scale(spiderRayFrontalLength * st.chain.chainLength()))
	}

	casts = append(casts,
		cast{frontal, frontalEnd, true},
		cast{pred.Add(up.Scale(spiderRayDownHeight)), pred.Add(up.Scale(-spiderRayDownDepth)), false},
		cast{def.Add(up.Scale(spiderRayDownHeight)), def.Add(up.Scale(-spiderRayDownDepth)), false},
	)

	bottom := spider.Body.Pos.Add(up.Scale(-spiderColliderRadius * 1.5))
	inEnd := bottom
	if d := pred.Sub(bottom); d.LenSq() > 1e-12 {
		inEnd = bottom.Add(d.Scale(spiderRayInwardsEnd))
	}
	casts = append(casts, cast{pred, inEnd, false})

	for _, c := range casts {
		dir := c.end.Sub(c.origin)
		maxDist := dir.Len()
		if maxDist < 1e-6 {
			continue
		}
		dir = dir.Scale(1 / maxDist)
		hit := CastFromFootWorld(world, c.origin, dir, maxDist, headY)
		if !hit.Hit {
			continue
		}
		if c.frontal {
			angle := math.Acos(clampScalar(dir.Dot(hit.Normal), -1, 1)) * 180 / math.Pi
			if angle < 180-65 {
				continue
			}
		}
		pt := hit.Point
		bodyHip := spiderBodyHip(spider.rootTransform())
		pt = clampFootToLegSector(pt, bodyHip, st.leg, spider.Heading)
		hip := spider.rootTransform().ToWorld(spider.rig.JointLocal(st.leg.Proximal))
		pt = clampFootToHipReach(pt, hip, spiderMaxHipPlantHoriz*0.96)
		return footTarget{Position: pt, Normal: hit.Normal, Grounded: true}
	}
	last := def.Add(up.Scale(spiderColliderRadius * 0.5))
	if world != nil {
		last.Y = world.GroundHeight(last.X, last.Z, headY)
	}
	return footTarget{Position: last, Normal: up, Grounded: true}
}

// spiderAsyncNeighbors returns diagonally opposite legs for async stepping.
func spiderAsyncNeighbors(legs []LegDef, idx int) []int {
	n := len(legs)
	if n < 4 {
		return nil
	}
	var out []int
	for _, d := range []int{2, n - 2} {
		j := (idx + d) % n
		if j != idx {
			out = append(out, j)
		}
	}
	return out
}
