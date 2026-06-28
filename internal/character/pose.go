package character

import (
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// SkeletonPose holds world-space bone frames after forward kinematics.
type SkeletonPose struct {
	Bones map[string]*scene.Transform
}

// ComputeFK builds world bone transforms for poseName at rootPos with rootYaw
// (degrees). rootPos is the hips joint in world space.
func (r *Rig) ComputeFK(poseName string, rootPos vec.V, rootYaw float64) SkeletonPose {
	out := SkeletonPose{Bones: make(map[string]*scene.Transform, len(r.BoneOrder))}
	for _, name := range r.BoneOrder {
		b := r.Bones[name]
		angles := r.PoseAngles(poseName, name)
		if b.Parent == "" {
			out.Bones[name] = scene.NewRigidTransform(0, rootYaw, 0, rootPos)
			continue
		}
		parent := out.Bones[b.Parent]
		jointLocal := r.JointLocal(name)
		out.Bones[name] = parent.ChildAt(jointLocal, angles.Pitch, angles.Yaw, angles.Roll)
	}
	return out
}

// BoneTip returns the world-space end of a bone segment (joint + local Y * length).
func (r *Rig) BoneTip(pose SkeletonPose, boneName string) vec.V {
	b := r.Bones[boneName]
	xf := pose.Bones[boneName]
	if xf == nil {
		return vec.V{}
	}
	return xf.ToWorld(vec.V{Y: b.Length})
}
