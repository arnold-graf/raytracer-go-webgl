package character

import (
	"math"
	"sort"

	"raytracer/internal/vec"
)

// Locomotion limits for ground sampling.
const (
	minWalkableNormalY   = 0.72 // reject surfaces steeper than ~44°
	footSeparationMargin = 0.02
)

func clampFootPlan(loc LocomotionParams, hip vec.V, target vec.V, fwd, right vec.V, sideSign, stride float64) vec.V {
	hipH := vec.V{X: hip.X, Z: hip.Z}
	rel := vec.V{X: target.X - hipH.X, Z: target.Z - hipH.Z}
	f := rel.Dot(fwd)
	lat := rel.Dot(right)

	if sideSign > 0 {
		lat = clamp(lat, loc.FootLateralMin, loc.FootLateralMax)
	} else {
		lat = clamp(lat, -loc.FootLateralMax, -loc.FootLateralMin)
	}

	maxFwd := stride * loc.StrideForwardLimit
	f = clamp(f, -maxFwd, maxFwd)

	out := hipH.Add(fwd.Scale(f)).Add(right.Scale(lat))
	out.Y = target.Y
	return out
}

func sampleWalkableGround(loc LocomotionParams, world FootWorld, x, z, refY, headY, floorBandY float64) (vec.V, bool) {
	if world == nil {
		return vec.V{X: x, Y: refY, Z: z}, true
	}
	y := world.GroundHeight(x, z, headY)
	y = clampFootHeight(loc, y, refY)
	if floorBandY > 1e-6 {
		maxY := floorBandY + loc.StepUp
		if y > maxY {
			y = maxY
		}
	}
	return vec.V{X: x, Y: y, Z: z}, true
}

func clampFootHeight(loc LocomotionParams, groundY, refY float64) float64 {
	if groundY-refY > loc.StepUp {
		return refY + loc.StepUp
	}
	if refY-groundY > loc.StepDown {
		return refY - loc.StepDown
	}
	return groundY
}

func (loc *Locomotor) footReferenceY(foot *Foot) float64 {
	if foot != nil && foot.Initialized && foot.Phase != FootSwing {
		return foot.PlantGroundY
	}
	if y := loc.plantedGroundY(); loc.anyFootInitialized() {
		return y
	}
	return loc.GroundY
}

func (loc *Locomotor) anyFootInitialized() bool {
	for _, f := range loc.plantedFeet() {
		if f.Initialized {
			return true
		}
	}
	return false
}

func (loc *Locomotor) partnerFootPos(sideSign float64) vec.V {
	if sideSign > 0 {
		return loc.rightFootRef()
	}
	return loc.leftFootRef()
}

func (loc *Locomotor) leftFootRef() vec.V {
	if loc.Left.Initialized {
		if loc.Left.Phase != FootSwing {
			return loc.Left.PlantWorld
		}
		return loc.Left.World
	}
	return loc.HipPos
}

func (loc *Locomotor) rightFootRef() vec.V {
	if loc.Right.Initialized {
		if loc.Right.Phase != FootSwing {
			return loc.Right.PlantWorld
		}
		return loc.Right.World
	}
	return loc.HipPos
}

// planLegTarget computes a footfall for one leg (biped or multiped).
func (loc *Locomotor) planLegTarget(rig *Rig, leg LegDef, phase, phaseOff float64, gait GaitParams, world FootWorld, hipBaseY float64, hip vec.V) vec.V {
	if leg.Kind == LegKindBiped {
		return loc.planFootTarget(rig, phase, hip, leg.SideSign, phaseOff, gait, world, hipBaseY)
	}
	return loc.planMultipedTarget(rig, leg, phase, phaseOff, gait, world, hipBaseY, hip)
}

func (loc *Locomotor) planMultipedTarget(rig *Rig, leg LegDef, phase, phaseOff float64, gait GaitParams, world FootWorld, hipBaseY float64, hip vec.V) vec.V {
	locParams := rig.Locomotion
	fwd := yawForward(loc.Heading)
	right := yawRight(loc.Heading)
	sideSign := leg.SideSign
	if sideSign == 0 {
		sideSign = legSideSignFromOffset(leg.RestOffset)
	}
	travel := gait.TravelSpeed(loc.Speed)
	stride := gait.StepStride(travel)

	rest := hipWorldOffset(hip, hipBaseY, loc.Heading, leg.RestOffset)

	if loc.Speed < 0.05 {
		rest.Y = world.GroundHeight(rest.X, rest.Z, groundHeadHint(loc, rig, hipBaseY))
		return enforceLegFootLane(locParams, rest, hip, right, leg)
	}

	fwdOff := legStrideOffset(phase, phaseOff, stride, leg.RestOffset.Z, locParams)
	target := rest.Add(fwd.Scale(fwdOff))
	target = clampFootPlan(locParams, hip, target, fwd, right, sideSign, stride)
	target = enforceLegFootLane(locParams, target, hip, right, leg)

	headY := groundHeadHint(loc, rig, hipBaseY)
	refY := loc.lowerPlantedGroundY()
	if sampled, ok := sampleWalkableGround(locParams, world, target.X, target.Z, refY, headY, loc.GroundY); ok {
		target = sampled
	} else {
		target.Y = clampFootHeight(locParams, refY, refY)
	}
	return enforceLegFootLane(locParams, target, hip, right, leg)
}

// planFootTarget computes a walk footfall with lane, height, and partner constraints.
func (loc *Locomotor) planFootTarget(rig *Rig, phase float64, hip vec.V, sideSign, phaseOff float64, gait GaitParams, world FootWorld, hipBaseY float64) vec.V {
	locParams := rig.Locomotion
	fwd, right := yawForward(loc.Heading), yawRight(loc.Heading)
	travel := gait.TravelSpeed(loc.Speed)
	stride := gait.StepStride(travel)
	fwdOff := -math.Cos((phase+phaseOff)*2*math.Pi) * stride * 0.5
	hipBase := vec.V{X: hip.X, Y: hipBaseY, Z: hip.Z}
	target := hipBase.Add(fwd.Scale(fwdOff)).Add(right.Scale(sideSign * locParams.FootOffsetLateral))

	if loc.Speed < 0.05 {
		// Idle: no stride, so feet sit directly below the hip with only lateral spread.
		// Using the full stride offset when idle makes the hip-to-ankle distance exceed
		// the IK chain length and the sole floats off the ground.
		target = hipBase.Add(right.Scale(sideSign * locParams.FootOffsetLateral))
		target.Y = world.GroundHeight(target.X, target.Z, groundHeadHint(loc, rig, hipBaseY))
		return target
	}

	target = clampFootPlan(locParams, hip, target, fwd, right, sideSign, stride)

	headY := groundHeadHint(loc, rig, hipBaseY)
	refY := loc.lowerPlantedGroundY()
	if sampled, ok := sampleWalkableGround(locParams, world, target.X, target.Z, refY, headY, loc.GroundY); ok {
		target = sampled
	} else {
		target.Y = clampFootHeight(locParams, refY, refY)
	}
	partner := loc.partnerFootPos(sideSign)
	target = enforceFootRelativeToPartner(locParams, target, partner, hip, right, sideSign)
	return target
}

func forwardAlongHeading(p, hip, fwd vec.V) float64 {
	return vec.V{X: p.X - hip.X, Z: p.Z - hip.Z}.Dot(fwd)
}

func (loc *Locomotor) enforceMultipedFootSeparation(rig *Rig) {
	locParams := rig.Locomotion
	legs := rig.LegDefs()
	loc.ensureFeet(len(legs))
	rightVec := yawRight(loc.Heading)
	fwd := yawForward(loc.Heading)
	hip := loc.HipPos
	minFwdGap := 0.18

	type legFoot struct {
		idx   int
		leg   LegDef
		restF float64
	}
	bySide := map[float64][]legFoot{}
	for i, leg := range legs {
		side := leg.SideSign
		if side == 0 {
			side = legSideSignFromOffset(leg.RestOffset)
		}
		bySide[side] = append(bySide[side], legFoot{
			idx:   i,
			leg:   leg,
			restF: -leg.RestOffset.Z,
		})
	}

	applyLane := func(p *vec.V, leg LegDef) {
		*p = enforceLegFootLane(locParams, *p, hip, rightVec, leg)
	}

	for _, group := range bySide {
		sort.Slice(group, func(i, j int) bool { return group[i].restF > group[j].restF })
		for _, item := range group {
			foot := &loc.Feet[item.idx]
			if !foot.Initialized {
				continue
			}
			applyLane(&foot.World, item.leg)
			if foot.Phase == FootSwing {
				applyLane(&foot.SwingTo, item.leg)
			} else {
				foot.PlantWorld = foot.World
				foot.PlantGroundY = foot.World.Y
			}
		}
		for k := 1; k < len(group); k++ {
			prev := &loc.Feet[group[k-1].idx]
			cur := &loc.Feet[group[k].idx]
			if !prev.Initialized || !cur.Initialized {
				continue
			}
			prevF := forwardAlongHeading(prev.World, hip, fwd)
			curF := forwardAlongHeading(cur.World, hip, fwd)
			if curF > prevF-minFwdGap {
				delta := curF - (prevF - minFwdGap)
				cur.World = cur.World.Add(fwd.Scale(-delta))
				applyLane(&cur.World, group[k].leg)
				if cur.Phase == FootSwing {
					cur.SwingTo = cur.SwingTo.Add(fwd.Scale(-delta))
					applyLane(&cur.SwingTo, group[k].leg)
				} else {
					cur.PlantWorld = cur.World
					cur.PlantGroundY = cur.World.Y
				}
			}
		}
	}
}

func slideLateral(p, hip, right vec.V, deltaLat float64) vec.V {
	return vec.V{X: p.X + right.X*deltaLat, Y: p.Y, Z: p.Z + right.Z*deltaLat}
}

func enforceLegFootLane(locParams LocomotionParams, p, hip, right vec.V, leg LegDef) vec.V {
	sideSign := leg.SideSign
	if sideSign == 0 {
		sideSign = legSideSignFromOffset(leg.RestOffset)
	}
	minLat := legMinLateral(locParams, leg)
	lat := lateralAlongRight(p, hip, right)
	if sideSign > 0 {
		if lat < minLat {
			return slideLateral(p, hip, right, minLat-lat)
		}
		if lat > locParams.FootLateralMax {
			return slideLateral(p, hip, right, locParams.FootLateralMax-lat)
		}
		return p
	}
	if lat > -minLat {
		return slideLateral(p, hip, right, -minLat-lat)
	}
	if lat < -locParams.FootLateralMax {
		return slideLateral(p, hip, right, -locParams.FootLateralMax-lat)
	}
	return p
}

func enforceFootLane(locParams LocomotionParams, p vec.V, hip, right vec.V, sideSign float64) vec.V {
	lat := lateralAlongRight(p, hip, right)
	if sideSign > 0 {
		if lat < locParams.FootLateralMin {
			p = slideLateral(p, hip, right, locParams.FootLateralMin-lat)
		} else if lat > locParams.FootLateralMax {
			p = slideLateral(p, hip, right, locParams.FootLateralMax-lat)
		}
	} else {
		if lat > -locParams.FootLateralMin {
			p = slideLateral(p, hip, right, -locParams.FootLateralMin-lat)
		} else if lat < -locParams.FootLateralMax {
			p = slideLateral(p, hip, right, -locParams.FootLateralMax-lat)
		}
	}
	return p
}

// enforceFootRelativeToPartner keeps a planned footfall on its lane and outside the other foot.
func enforceFootRelativeToPartner(locParams LocomotionParams, target, partner, hip, right vec.V, sideSign float64) vec.V {
	target = enforceFootLane(locParams, target, hip, right, sideSign)
	targetLat := lateralAlongRight(target, hip, right)
	partnerLat := lateralAlongRight(partner, hip, right)
	minPairSep := locParams.FootPairSep()
	if sideSign > 0 {
		need := partnerLat + minPairSep
		if targetLat < need {
			target = slideLateral(target, hip, right, need-targetLat)
		}
	} else {
		need := partnerLat - minPairSep
		if targetLat > need {
			target = slideLateral(target, hip, right, need-targetLat)
		}
	}
	return enforceFootLane(locParams, target, hip, right, sideSign)
}

func enforcePairwiseFeet(locParams LocomotionParams, left, right *vec.V, hip, rightVec vec.V) {
	if left == nil || right == nil {
		return
	}
	*left = enforceFootLane(locParams, *left, hip, rightVec, 1)
	*right = enforceFootLane(locParams, *right, hip, rightVec, -1)
	lLat := lateralAlongRight(*left, hip, rightVec)
	rLat := lateralAlongRight(*right, hip, rightVec)
	if gap := locParams.FootPairSep() - (lLat - rLat); gap > 0 {
		shift := gap * 0.5
		*left = slideLateral(*left, hip, rightVec, shift)
		*right = slideLateral(*right, hip, rightVec, -shift)
	}
}

// enforceFootSeparation applies foot-lane invariants after each locomotion tick.
func (loc *Locomotor) enforceFootSeparation(rig *Rig) {
	locParams := rig.Locomotion
	legs := rig.LegDefs()
	if loc.Speed < 0.05 || len(legs) == 0 {
		return
	}
	loc.ensureFeet(len(legs))

	if rig.isMultiped() {
		loc.enforceMultipedFootSeparation(rig)
		return
	}

	if !loc.Feet[0].Initialized || !loc.Feet[1].Initialized {
		return
	}
	rightVec := yawRight(loc.Heading)
	hip := loc.HipPos

	enforcePairwiseFeet(locParams, &loc.Feet[0].World, &loc.Feet[1].World, hip, rightVec)

	if loc.Feet[0].Phase == FootSwing {
		loc.Feet[0].SwingTo = enforceFootRelativeToPartner(locParams, loc.Feet[0].SwingTo, loc.footRefAt(1), hip, rightVec, 1)
	}
	if loc.Feet[1].Phase == FootSwing {
		loc.Feet[1].SwingTo = enforceFootRelativeToPartner(locParams, loc.Feet[1].SwingTo, loc.footRefAt(0), hip, rightVec, -1)
	}

	for i := range 2 {
		if loc.Feet[i].Phase != FootSwing {
			loc.Feet[i].PlantWorld = loc.Feet[i].World
			loc.Feet[i].PlantGroundY = loc.Feet[i].World.Y
		}
	}
}

func (loc *Locomotor) footRefAt(i int) vec.V {
	if i < len(loc.Feet) && loc.Feet[i].Initialized {
		if loc.Feet[i].Phase != FootSwing {
			return loc.Feet[i].PlantWorld
		}
		return loc.Feet[i].World
	}
	return loc.HipPos
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// legStrideOffset returns the forward/back foot excursion along travel.
func legStrideOffset(phase, phaseOff, stride, restZ float64, locParams LocomotionParams) float64 {
	raw := -math.Cos((phase+phaseOff)*2*math.Pi) * stride * 0.58
	bias := stride * 0.10
	if restZ > 0.1 {
		bias += stride * 0.06 * clamp(restZ/0.5, 0.3, 0.8)
	}
	maxBack := stride * locParams.StrideForwardLimit * 0.06
	maxFwd := stride * locParams.StrideForwardLimit
	return clamp(raw+bias, -maxBack, maxFwd)
}

func lateralAlongRight(p, hip vec.V, right vec.V) float64 {
	return vec.V{X: p.X - hip.X, Z: p.Z - hip.Z}.Dot(right)
}

// FeetCrossed reports whether left/right contacts have swapped sides.
func FeetCrossed(locParams LocomotionParams, left, right vec.V, hip vec.V, heading float64) bool {
	rightVec := yawRight(heading)
	lLat := lateralAlongRight(left, hip, rightVec)
	rLat := lateralAlongRight(right, hip, rightVec)
	return lLat < rLat+footSeparationMargin
}

func footSeparationOK(locParams LocomotionParams, left, right vec.V, hip vec.V, heading float64) bool {
	rightVec := yawRight(heading)
	lLat := lateralAlongRight(left, hip, rightVec)
	rLat := lateralAlongRight(right, hip, rightVec)
	return lLat >= locParams.FootLateralMin-footSeparationMargin &&
		rLat <= -locParams.FootLateralMin+footSeparationMargin &&
		lLat-rLat >= locParams.FootPairSep()-footSeparationMargin
}

func kneeLateral(rig *Rig, pose SkeletonPose, shinName string, hip, rightVec vec.V) float64 {
	shinXF := pose.Bones[shinName]
	if shinXF == nil {
		return 0
	}
	return lateralAlongRight(shinXF.Translation(), hip, rightVec)
}

func kneesCrossed(rig *Rig, pose SkeletonPose, hip vec.V, heading float64) bool {
	rightVec := yawRight(heading)
	lK := kneeLateral(rig, pose, "shin_l", hip, rightVec)
	rK := kneeLateral(rig, pose, "shin_r", hip, rightVec)
	return lK < rK+footSeparationMargin
}

func legIKPole(locParams LocomotionParams, hipSocket, ankleTarget vec.V, fwd, right vec.V, sideSign float64, foot Foot) vec.V {
	drop := hipSocket.Y - ankleTarget.Y
	poleFwd := 0.42
	poleLat := 0.16 * sideSign

	// During swing or when the foot sits well below the hip, bias the pole forward so
	// knee flex reads clearly from the side (stairs / lift). On flat stance use a
	// wider lateral pole to prevent crossing.
	if foot.Phase == FootSwing || drop > 0.08 {
		poleFwd = 0.50
		poleLat = 0.12 * sideSign
		if footStepUp(foot.PlantWorld, foot.SwingTo) > locParams.StepUpMinHeight {
			intensity := stepUpIntensity(footStepUp(foot.PlantWorld, foot.SwingTo), foot.PlantGroundY, locParams)
			poleFwd = 0.50 + 0.04*intensity
			poleLat = (0.12 - 0.02*intensity) * sideSign
		}
	} else {
		poleLat = 0.24 * sideSign
		poleFwd = 0.40
	}

	ankleLat := lateralAlongRight(ankleTarget, hipSocket, right)
	if sideSign > 0 {
		if poleLat < ankleLat+locParams.Knee.MinOutward {
			poleLat = ankleLat + locParams.Knee.MinOutward
		}
		if poleLat < 0.20 {
			poleLat = 0.20
		}
	} else {
		if poleLat > ankleLat-locParams.Knee.MinOutward {
			poleLat = ankleLat - locParams.Knee.MinOutward
		}
		if poleLat > -0.20 {
			poleLat = -0.20
		}
	}
	return hipSocket.Add(fwd.Scale(poleFwd)).Add(right.Scale(poleLat))
}

func footArcInLane(locParams LocomotionParams, from, to vec.V, lift, t float64, hip, right vec.V, sideSign float64) vec.V {
	return enforceFootLane(locParams, footArcBezier(from, to, lift, t), hip, right, sideSign)
}

func footSwingArcMultiped(from, to vec.V, baseLift, t float64, hip vec.V, right vec.V, leg LegDef, locParams LocomotionParams) vec.V {
	stepUp := footStepUp(from, to)
	lift := swingLift(from, to, baseLift*1.15, locParams)
	var p vec.V
	if stepUp > locParams.StepUpMinHeight {
		p = footArcStepUp(from, to, lift, t, stepUp, locParams)
	} else {
		p = footArcBezier(from, to, lift, t)
	}
	return enforceLegFootLane(locParams, p, hip, right, leg)
}

func footSwingArc(from, to vec.V, baseLift, t float64, hip, right vec.V, sideSign float64, locParams LocomotionParams) vec.V {
	stepUp := footStepUp(from, to)
	lift := swingLift(from, to, baseLift, locParams)
	var p vec.V
	if stepUp > locParams.StepUpMinHeight {
		p = footArcStepUp(from, to, lift, t, stepUp, locParams)
	} else {
		p = footArcBezier(from, to, lift, t)
	}
	return enforceFootLane(locParams, p, hip, right, sideSign)
}

func swingLift(from, to vec.V, baseLift float64, locParams LocomotionParams) float64 {
	lift := baseLift
	stepUp := footStepUp(from, to)
	if stepUp > locParams.StepUpMinHeight {
		intensity := stepUpIntensity(stepUp, from.Y, locParams)
		lift += stepUp*(0.55+0.25*intensity) + 0.03*intensity
	}
	return clamp(lift, baseLift, 0.40)
}

// footArcStepUp lifts the foot at the start of swing so the knee bends before the pelvis rises.
func footArcStepUp(from, to vec.V, lift, t, stepUp float64, locParams LocomotionParams) vec.V {
	p := footArcBezier(from, to, lift, t)
	intensity := stepUpIntensity(stepUp, from.Y, locParams)
	early := math.Sin(t * math.Pi)
	firstHalf := 1 - t
	boost := intensity * (stepUp*(0.22*early+0.18*firstHalf) + 0.03*early)
	p.Y += boost
	return p
}

func footArcBezier(from, to vec.V, lift, t float64) vec.V {
	mid := from.Add(to).Scale(0.5)
	mid.Y += lift
	u := 1 - t
	return from.Scale(u * u).Add(mid.Scale(2 * u * t)).Add(to.Scale(t * t))
}
