package character

import (
	"math"

	"raytracer/internal/vec"
)

// Locomotion limits for foot placement and ground sampling.
const (
	maxFootStepUp        = 0.50 // max upward footfall per step (m)
	maxFootStepDown      = 1.20 // max downward footfall per step (m)
	minFootLateral       = 0.08 // minimum distance from hip centerline (m)
	maxFootLateral       = 0.17 // maximum lateral spread (m)
	minFootPairSep       = 2 * minFootLateral
	minWalkableNormalY   = 0.72 // reject surfaces steeper than ~44°
	footSeparationMargin = 0.02
	minKneeOutward       = 0.06 // minimum knee offset from hip centerline (m)
)

func clampFootPlan(hip vec.V, target vec.V, fwd, right vec.V, sideSign, stride float64) vec.V {
	hipH := vec.V{X: hip.X, Z: hip.Z}
	rel := vec.V{X: target.X - hipH.X, Z: target.Z - hipH.Z}
	f := rel.Dot(fwd)
	lat := rel.Dot(right)

	if sideSign > 0 {
		lat = clamp(lat, minFootLateral, maxFootLateral)
	} else {
		lat = clamp(lat, -maxFootLateral, -minFootLateral)
	}

	maxFwd := stride * 0.55
	f = clamp(f, -maxFwd, maxFwd)

	out := hipH.Add(fwd.Scale(f)).Add(right.Scale(lat))
	out.Y = target.Y
	return out
}

func sampleWalkableGround(world FootWorld, x, z, refY, headY, floorBandY float64) (vec.V, bool) {
	if world == nil {
		return vec.V{X: x, Y: refY, Z: z}, true
	}
	y := world.GroundHeight(x, z, headY)
	y = clampFootHeight(y, refY)
	if floorBandY > 1e-6 {
		maxY := floorBandY + maxFootStepUp
		if y > maxY {
			y = maxY
		}
	}
	return vec.V{X: x, Y: y, Z: z}, true
}

func clampFootHeight(groundY, refY float64) float64 {
	if groundY-refY > maxFootStepUp {
		return refY + maxFootStepUp
	}
	if refY-groundY > maxFootStepDown {
		return refY - maxFootStepDown
	}
	return groundY
}

func (loc *Locomotor) footReferenceY(foot *Foot) float64 {
	if foot != nil && foot.Initialized && foot.Phase != FootSwing {
		return foot.PlantGroundY
	}
	if y := loc.plantedGroundY(); loc.Left.Initialized || loc.Right.Initialized {
		return y
	}
	return loc.GroundY
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

// planFootTarget computes a walk footfall with lane, height, and partner constraints.
func (loc *Locomotor) planFootTarget(phase float64, hip vec.V, sideSign, phaseOff float64, gait GaitParams, world FootWorld, hipBaseY float64) vec.V {
	fwd, right := yawForward(loc.Heading), yawRight(loc.Heading)
	travel := gait.TravelSpeed(loc.Speed)
	stride := gait.StepStride(travel)
	fwdOff := -math.Cos((phase+phaseOff)*2*math.Pi) * stride * 0.5
	hipBase := vec.V{X: hip.X, Y: hipBaseY, Z: hip.Z}
	target := hipBase.Add(fwd.Scale(fwdOff)).Add(right.Scale(sideSign * 0.14))

	if loc.Speed < 0.05 {
		// Idle: no stride, so feet sit directly below the hip with only lateral spread.
		// Using the full stride offset when idle makes the hip-to-ankle distance exceed
		// the IK chain length and the sole floats off the ground.
		target = hipBase.Add(right.Scale(sideSign * 0.14))
		target.Y = world.GroundHeight(target.X, target.Z, groundHeadHint(loc, hipBaseY))
		return target
	}

	target = clampFootPlan(hip, target, fwd, right, sideSign, stride)

	headY := groundHeadHint(loc, hipBaseY)
	refY := loc.lowerPlantedGroundY()
	if sampled, ok := sampleWalkableGround(world, target.X, target.Z, refY, headY, loc.GroundY); ok {
		target = sampled
	} else {
		target.Y = clampFootHeight(refY, refY)
	}
	partner := loc.partnerFootPos(sideSign)
	target = enforceFootRelativeToPartner(target, partner, hip, right, sideSign)
	return target
}

func slideLateral(p, hip, right vec.V, deltaLat float64) vec.V {
	return vec.V{X: p.X + right.X*deltaLat, Y: p.Y, Z: p.Z + right.Z*deltaLat}
}

func enforceFootLane(p vec.V, hip, right vec.V, sideSign float64) vec.V {
	lat := lateralAlongRight(p, hip, right)
	if sideSign > 0 {
		if lat < minFootLateral {
			p = slideLateral(p, hip, right, minFootLateral-lat)
		} else if lat > maxFootLateral {
			p = slideLateral(p, hip, right, maxFootLateral-lat)
		}
	} else {
		if lat > -minFootLateral {
			p = slideLateral(p, hip, right, -minFootLateral-lat)
		} else if lat < -maxFootLateral {
			p = slideLateral(p, hip, right, -maxFootLateral-lat)
		}
	}
	return p
}

// enforceFootRelativeToPartner keeps a planned footfall on its lane and outside the other foot.
func enforceFootRelativeToPartner(target, partner, hip, right vec.V, sideSign float64) vec.V {
	target = enforceFootLane(target, hip, right, sideSign)
	targetLat := lateralAlongRight(target, hip, right)
	partnerLat := lateralAlongRight(partner, hip, right)
	if sideSign > 0 {
		need := partnerLat + minFootPairSep
		if targetLat < need {
			target = slideLateral(target, hip, right, need-targetLat)
		}
	} else {
		need := partnerLat - minFootPairSep
		if targetLat > need {
			target = slideLateral(target, hip, right, need-targetLat)
		}
	}
	return enforceFootLane(target, hip, right, sideSign)
}

func enforcePairwiseFeet(left, right *vec.V, hip, rightVec vec.V) {
	if left == nil || right == nil {
		return
	}
	*left = enforceFootLane(*left, hip, rightVec, 1)
	*right = enforceFootLane(*right, hip, rightVec, -1)
	lLat := lateralAlongRight(*left, hip, rightVec)
	rLat := lateralAlongRight(*right, hip, rightVec)
	if gap := minFootPairSep - (lLat - rLat); gap > 0 {
		shift := gap * 0.5
		*left = slideLateral(*left, hip, rightVec, shift)
		*right = slideLateral(*right, hip, rightVec, -shift)
	}
}

// enforceFootSeparation applies the pairwise foot-lane invariant after each locomotion tick.
func (loc *Locomotor) enforceFootSeparation() {
	if loc.Speed < 0.05 || !loc.Left.Initialized || !loc.Right.Initialized {
		return
	}
	rightVec := yawRight(loc.Heading)
	hip := loc.HipPos

	enforcePairwiseFeet(&loc.Left.World, &loc.Right.World, hip, rightVec)

	if loc.Left.Phase == FootSwing {
		loc.Left.SwingTo = enforceFootRelativeToPartner(loc.Left.SwingTo, loc.rightFootRef(), hip, rightVec, 1)
	}
	if loc.Right.Phase == FootSwing {
		loc.Right.SwingTo = enforceFootRelativeToPartner(loc.Right.SwingTo, loc.leftFootRef(), hip, rightVec, -1)
	}

	if loc.Left.Phase != FootSwing {
		loc.Left.PlantWorld = loc.Left.World
		loc.Left.PlantGroundY = loc.Left.World.Y
	}
	if loc.Right.Phase != FootSwing {
		loc.Right.PlantWorld = loc.Right.World
		loc.Right.PlantGroundY = loc.Right.World.Y
	}
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

func lateralAlongRight(p, hip vec.V, right vec.V) float64 {
	return vec.V{X: p.X - hip.X, Z: p.Z - hip.Z}.Dot(right)
}

// FeetCrossed reports whether left/right contacts have swapped sides.
func FeetCrossed(left, right vec.V, hip vec.V, heading float64) bool {
	rightVec := yawRight(heading)
	lLat := lateralAlongRight(left, hip, rightVec)
	rLat := lateralAlongRight(right, hip, rightVec)
	return lLat < rLat+footSeparationMargin
}

func footSeparationOK(left, right vec.V, hip vec.V, heading float64) bool {
	rightVec := yawRight(heading)
	lLat := lateralAlongRight(left, hip, rightVec)
	rLat := lateralAlongRight(right, hip, rightVec)
	return lLat >= minFootLateral-footSeparationMargin &&
		rLat <= -minFootLateral+footSeparationMargin &&
		lLat-rLat >= minFootPairSep-footSeparationMargin
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

func legIKPole(hipSocket, ankleTarget vec.V, fwd, right vec.V, sideSign float64, foot Foot) vec.V {
	drop := hipSocket.Y - ankleTarget.Y
	poleFwd := 0.42
	poleLat := 0.16 * sideSign

	// During swing or when the foot sits well below the hip, bias the pole forward so
	// knee flex reads clearly from the side (stairs / lift). On flat stance use a
	// wider lateral pole to prevent crossing.
	if foot.Phase == FootSwing || drop > 0.08 {
		poleFwd = 0.50
		poleLat = 0.12 * sideSign
		if footStepUp(foot.PlantWorld, foot.SwingTo) > stepUpMinHeight {
			intensity := stepUpIntensity(footStepUp(foot.PlantWorld, foot.SwingTo), foot.PlantGroundY)
			poleFwd = 0.50 + 0.04*intensity
			poleLat = (0.12 - 0.02*intensity) * sideSign
		}
	} else {
		poleLat = 0.24 * sideSign
		poleFwd = 0.40
	}

	ankleLat := lateralAlongRight(ankleTarget, hipSocket, right)
	if sideSign > 0 {
		if poleLat < ankleLat+minKneeOutward {
			poleLat = ankleLat + minKneeOutward
		}
		if poleLat < 0.20 {
			poleLat = 0.20
		}
	} else {
		if poleLat > ankleLat-minKneeOutward {
			poleLat = ankleLat - minKneeOutward
		}
		if poleLat > -0.20 {
			poleLat = -0.20
		}
	}
	return hipSocket.Add(fwd.Scale(poleFwd)).Add(right.Scale(poleLat))
}

func footArcInLane(from, to vec.V, lift, t float64, hip, right vec.V, sideSign float64) vec.V {
	return enforceFootLane(footArcBezier(from, to, lift, t), hip, right, sideSign)
}

func footSwingArc(from, to vec.V, baseLift, t float64, hip, right vec.V, sideSign float64) vec.V {
	stepUp := footStepUp(from, to)
	lift := swingLift(from, to, baseLift)
	var p vec.V
	if stepUp > stepUpMinHeight {
		p = footArcStepUp(from, to, lift, t, stepUp)
	} else {
		p = footArcBezier(from, to, lift, t)
	}
	return enforceFootLane(p, hip, right, sideSign)
}

func swingLift(from, to vec.V, baseLift float64) float64 {
	lift := baseLift
	stepUp := footStepUp(from, to)
	if stepUp > stepUpMinHeight {
		intensity := stepUpIntensity(stepUp, from.Y)
		lift += stepUp*(0.55+0.25*intensity) + 0.03*intensity
	}
	return clamp(lift, baseLift, 0.40)
}

// footArcStepUp lifts the foot at the start of swing so the knee bends before the pelvis rises.
func footArcStepUp(from, to vec.V, lift, t, stepUp float64) vec.V {
	p := footArcBezier(from, to, lift, t)
	intensity := stepUpIntensity(stepUp, from.Y)
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
