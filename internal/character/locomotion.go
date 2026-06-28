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
	if world != nil {
		headY := loc.HipPos.Y + 0.5
		gy := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, headY)
		hips = HipPositionFromGround(loc.HipPos.X, gy, loc.HipPos.Z, rig.HipHeight)
	}

	if loc.Speed < 0.05 && world == nil {
		return rig.ComputeFK(poseName, hips, loc.Heading)
	}

	upperPose := poseName
	if loc.Speed >= 0.05 {
		if _, ok := rig.Poses["walk"]; ok {
			upperPose = "walk"
		}
	}
	phase := 0.0
	if loc.Speed >= 0.05 {
		phase = loc.Phase
	}
	pose := rig.computeUpperBodyPose(upperPose, loc, phase)
	if world == nil {
		return rig.ComputeFK(poseName, hips, loc.Heading)
	}

	applyLegIK(rig, &pose, "thigh_l", "shin_l", "foot_l", loc.Left.World, loc.Heading, 1, world)
	applyLegIK(rig, &pose, "thigh_r", "shin_r", "foot_r", loc.Right.World, loc.Heading, -1, world)
	return pose
}

func (r *Rig) computeUpperBodyPose(poseName string, loc *Locomotor, phase float64) SkeletonPose {
	// Arm swing opposes leg stride (left arm back when left foot forward).
	stridePhase := math.Cos(phase * 2 * math.Pi)
	swing := stridePhase * 12
	sway := math.Sin(phase * 2 * math.Pi)

	skipLeg := map[string]bool{
		"thigh_l": true, "shin_l": true, "foot_l": true,
		"thigh_r": true, "shin_r": true, "foot_r": true,
	}

	hips := loc.HipPos
	latSway := yawRight(loc.Heading).Scale(sway * 0.025)
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
			angles.Pitch += sway * 1.5
			angles.Roll += sway * 2
		case "upper_arm_l":
			angles.Pitch += swing
		case "upper_arm_r":
			angles.Pitch -= swing
		}
		if b.Parent == "" {
			out.Bones[name] = scene.NewRigidTransform(sway, loc.Heading, sway*1.5, hips)
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

func applyLegIK(rig *Rig, pose *SkeletonPose, thighName, shinName, footName string, contact vec.V, heading, sideSign float64, world FootWorld) {
	hips := pose.Bones["hips"]
	if hips == nil {
		return
	}
	hipSocket := hips.ToWorld(rig.JointLocal(thighName))
	thigh := rig.Bones[thighName]
	shin := rig.Bones[shinName]

	normal := vec.V{Y: 1}
	if world != nil {
		normal = world.GroundNormal(contact.X, contact.Z, contact.Y+0.5)
		if normal.LenSq() < 1e-12 {
			normal = vec.V{Y: 1}
		} else {
			normal = normal.Normalize()
		}
	}
	ankle := anklePosition(contact, normal, rig.AnkleHeight)

	fwd := yawForward(heading)
	right := yawRight(heading)
	pole := hipSocket.Add(fwd.Scale(0.42)).Add(right.Scale(0.12 * sideSign))

	res := SolveTwoBone(hipSocket, ankle, pole, thigh.Length, shin.Length)
	if !res.OK {
		return
	}
	pose.Bones[thighName] = scene.NewTransformYAxis(hipSocket, res.Mid)
	pose.Bones[shinName] = scene.NewTransformYAxis(res.Mid, ankle)
	applyFootPlant(rig, pose, footName, ankle, contact, normal, heading)
}
