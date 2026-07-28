package character

import (
	"math"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func anklePosition(contact, normal vec.V, soleBelowAnkle float64) vec.V {
	return contact.Add(normal.Scale(soleBelowAnkle))
}

// footSoleBelowAnkle is the distance from the ankle joint to the sole along the
// foot's local +Z (surface normal when planted flat).
func footSoleBelowAnkle(rig *Rig, footName string) float64 {
	for i := range rig.Attachments {
		att := &rig.Attachments[i]
		if att.Bone != footName || att.Kind != "box" {
			continue
		}
		soleZ := att.Offset.Z - att.Size.Z*0.5
		return -soleZ
	}
	if rig.AnkleHeight > 0 {
		return rig.AnkleHeight
	}
	return 0.05
}

func footAttachment(rig *Rig, footName string) *Attachment {
	for i := range rig.Attachments {
		if rig.Attachments[i].Bone == footName {
			return &rig.Attachments[i]
		}
	}
	return nil
}

func projectOnPlane(v, up vec.V) vec.V {
	return v.Sub(up.Scale(v.Dot(up)))
}

func footForwardOnPlane(heading float64, up vec.V) vec.V {
	fwd := projectOnPlane(yawForward(heading), up)
	if fwd.LenSq() < 1e-12 {
		fwd = projectOnPlane(yawRight(heading), up)
	}
	if fwd.LenSq() < 1e-12 {
		return vec.V{X: 1}
	}
	return fwd.Normalize()
}

func footGroundNormal(foot Foot, world FootWorld) vec.V {
	if world == nil {
		return vec.V{Y: 1}
	}
	// In flight, keep the foot level until it is about to land.
	if foot.Phase == FootSwing && foot.SwingT < 0.85 {
		return vec.V{Y: 1}
	}
	if foot.Phase != FootSwing && foot.PlantNormal.LenSq() > 0.5 {
		return foot.PlantNormal
	}
	p := foot.World
	n := world.GroundNormal(p.X, p.Z, p.Y+0.5)
	if n.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	return n.Normalize()
}

func applyFootPlant(rig *Rig, pose *SkeletonPose, footName string, ankle, contact vec.V, normal vec.V, heading float64, phase FootPhase, stanceT float64) {
	up := normal
	if up.LenSq() < 1e-12 {
		up = vec.V{Y: 1}
	} else {
		up = up.Normalize()
	}
	fwd := footForwardOnPlane(heading, up)
	base := scene.NewTransformYZ(ankle, fwd, up)
	roll := footRollDeg(phase, stanceT, rig.Locomotion.FootRoll)
	if roll != 0 {
		rollXF := scene.NewRigidTransform(roll, 0, 0, vec.V{})
		base = base.Compose(rollXF)
	}
	pose.Bones[footName] = base
}

// footSoleWorld returns the lowest corner of the foot box attachment in world space.
func footSoleWorld(r *Rig, pose SkeletonPose, footName string) vec.V {
	att := footAttachment(r, footName)
	if att == nil || pose.Bones[footName] == nil {
		return vec.V{}
	}
	xf := pose.Bones[footName]
	half := att.Size.Scale(0.5)
	minLocal := att.Offset.Sub(half)
	corners := []vec.V{
		minLocal,
		minLocal.Add(vec.V{X: att.Size.X}),
		minLocal.Add(vec.V{Y: att.Size.Y}),
		minLocal.Add(vec.V{Z: att.Size.Z}),
		minLocal.Add(vec.V{X: att.Size.X, Y: att.Size.Y}),
		minLocal.Add(vec.V{X: att.Size.X, Z: att.Size.Z}),
		minLocal.Add(vec.V{Y: att.Size.Y, Z: att.Size.Z}),
		minLocal.Add(att.Size),
	}
	soleY := math.Inf(1)
	var sole vec.V
	for _, c := range corners {
		w := xf.ToWorld(c)
		if w.Y < soleY {
			soleY = w.Y
			sole = w
		}
	}
	return sole
}
