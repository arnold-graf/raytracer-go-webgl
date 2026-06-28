package character

import (
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func anklePosition(contact, normal vec.V, ankleHeight float64) vec.V {
	return contact.Add(normal.Scale(ankleHeight))
}

func footToePoint(contact, normal vec.V, heading, footLen float64) vec.V {
	fwd := yawForward(heading)
	fTan := fwd.Sub(normal.Scale(fwd.Dot(normal)))
	if fTan.LenSq() < 1e-12 {
		return contact.Add(normal.Scale(0.01))
	}
	fTan = fTan.Normalize()
	return contact.Add(fTan.Scale(footLen * 0.55))
}

func applyFootPlant(rig *Rig, pose *SkeletonPose, footName string, ankle, contact, normal vec.V, heading float64) {
	footLen := rig.Bones[footName].Length
	if footLen <= 0 {
		footLen = 0.10
	}
	toe := footToePoint(contact, normal, heading, footLen)
	pose.Bones[footName] = scene.NewTransformYAxis(ankle, toe)
}
