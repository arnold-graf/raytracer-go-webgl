package character

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// ComputeLocomotionPose builds a full skeleton pose with IK legs and swaying
// upper body for the current locomotor state.
func ComputeLocomotionPose(rig *Rig, loc *Locomotor, poseName string, world FootWorld) SkeletonPose {
	hips := loc.HipPos

	if loc.Speed < 0.05 {
		return rig.ComputeFK(poseName, hips, loc.Heading)
	}

	legs := rig.LegDefs()
	if len(legs) == 0 || world == nil {
		return rig.ComputeFK(poseName, hips, loc.Heading)
	}

	if rig.isMultiped() {
		pose := rig.computeMultipedBodyPose(poseName, loc, loc.Phase)
		for i, leg := range legs {
			loc.ensureFeet(len(legs))
			applyTripodLegIK(rig, &pose, leg, &loc.Feet[i], loc.Heading)
		}
		return pose
	}

	upperPose := poseName
	if _, ok := rig.Poses["walk"]; ok {
		upperPose = "walk"
	}
	phase := loc.Phase
	pose := rig.computeUpperBodyPose(upperPose, loc, phase)
	loc.ensureFeet(len(legs))
	applyLegIK(rig, &pose, "thigh_l", "shin_l", "foot_l", loc.Feet[0], loc.Heading, 1, world)
	applyLegIK(rig, &pose, "thigh_r", "shin_r", "foot_r", loc.Feet[1], loc.Heading, -1, world)
	return pose
}

func (r *Rig) hasBipedLegs() bool {
	if r == nil {
		return false
	}
	_, ok := r.Bones["thigh_l"]
	return ok
}

func (r *Rig) computeMultipedBodyPose(poseName string, loc *Locomotor, phase float64) SkeletonPose {
	skipLeg := r.legBoneSet()
	gait := r.GaitForSpeed(loc.Speed)
	bob := math.Sin(phase*2*math.Pi) * gait.Bob
	hips := loc.HipPos.Add(vec.V{Y: bob})

	out := SkeletonPose{Bones: make(map[string]*scene.Transform, len(r.BoneOrder))}
	for _, name := range r.BoneOrder {
		if skipLeg[name] {
			continue
		}
		b := r.Bones[name]
		angles := r.PoseAngles(poseName, name)
		if b.Parent == "" {
			out.Bones[name] = scene.NewRigidTransform(loc.BodyPitch, loc.Heading, loc.BodyRoll, hips)
			continue
		}
		parent := out.Bones[b.Parent]
		if parent == nil {
			continue
		}
		jointLocal := r.JointLocal(name)
		out.Bones[name] = parent.ChildAt(jointLocal, angles.Pitch, angles.Yaw, angles.Roll)
	}
	return out
}

func (r *Rig) computeUpperBodyPose(poseName string, loc *Locomotor, phase float64) SkeletonPose {
	// Arm swing opposes leg stride (left arm back when left foot forward).
	swayParams := r.Locomotion.UpperBody
	stridePhase := math.Cos(phase * 2 * math.Pi)
	swing := stridePhase * swayParams.ArmSwing
	sway := math.Sin(phase * 2 * math.Pi)

	skipLeg := r.legBoneSet()

	hips := loc.HipPos
	latSway := yawRight(loc.Heading).Scale(sway * swayParams.LateralSway)
	hips = hips.Add(latSway)

	out := SkeletonPose{Bones: make(map[string]*scene.Transform, len(r.BoneOrder))}
	for _, name := range r.BoneOrder {
		if skipLeg[name] {
			continue
		}
		b := r.Bones[name]
		angles := r.PoseAngles(poseName, name)
		switch name {
		case "spine":
			angles.Pitch += sway * swayParams.SpinePitch
			angles.Roll += sway * swayParams.SpineRoll
		case "upper_arm_l":
			angles.Pitch += swing
		case "upper_arm_r":
			angles.Pitch -= swing
		}
		if b.Parent == "" {
			out.Bones[name] = scene.NewRigidTransform(sway, loc.Heading, sway*swayParams.HipRoll, hips)
			continue
		}
		parent := out.Bones[b.Parent]
		if parent == nil {
			continue
		}
		jointLocal := r.JointLocal(name)
		out.Bones[name] = parent.ChildAt(jointLocal, angles.Pitch, angles.Yaw, angles.Roll)
	}
	return out
}

func applyLegIK(rig *Rig, pose *SkeletonPose, thighName, shinName, footName string, foot Foot, heading, sideSign float64, world FootWorld) {
	contact := foot.World
	hips := pose.Bones["hips"]
	if hips == nil {
		return
	}
	hipSocket := hips.ToWorld(rig.JointLocal(thighName))
	thigh := rig.Bones[thighName]
	shin := rig.Bones[shinName]
	locParams := rig.Locomotion
	knee := locParams.Knee

	normal := footGroundNormal(foot, world)
	soleDrop := footSoleBelowAnkle(rig, footName)
	ankleTarget := anklePosition(contact, normal, soleDrop)

	fwd := yawForward(heading)
	right := yawRight(heading)
	pole := legIKPole(locParams, hipSocket, ankleTarget, fwd, right, sideSign, foot)

	minBend := knee.Stance
	stepUp := footStepUp(foot.PlantWorld, foot.SwingTo)
	if foot.Phase == FootSwing {
		minBend = knee.Swing
		if stepUp > locParams.StepUpMinHeight {
			intensity := stepUpIntensity(stepUp, foot.PlantGroundY, locParams)
			minBend = knee.StepUpBase + knee.StepUpScale*intensity
		} else if ankleTarget.Y >= hipSocket.Y-0.08 {
			minBend = knee.HighAnkle
		}
	} else if hipSocket.Y-ankleTarget.Y > 0.10 {
		minBend = knee.StanceDeep
	}

	res := SolveTwoBoneMinBend(hipSocket, ankleTarget, pole, thigh.Length, shin.Length, minBend)
	if !res.OK {
		return
	}
	fallbackBend := knee.FallbackStance
	if foot.Phase == FootSwing && stepUp > locParams.StepUpMinHeight {
		intensity := stepUpIntensity(stepUp, foot.PlantGroundY, locParams)
		fallbackBend = knee.FallbackStepUpBase + knee.FallbackStepUpScale*intensity
	}
	if res.EndError(ankleTarget) > 0.015 {
		if loose := SolveTwoBoneMinBend(hipSocket, ankleTarget, pole, thigh.Length, shin.Length, fallbackBend); loose.OK {
			res = loose
		}
	}
	ikAnkle := res.End
	pose.Bones[thighName] = scene.NewTransformYAxis(hipSocket, res.Mid)
	pose.Bones[shinName] = scene.NewTransformYAxis(res.Mid, ikAnkle)
	applyFootPlant(rig, pose, footName, ikAnkle, contact, normal, heading, foot.Phase, foot.StanceT)
}
