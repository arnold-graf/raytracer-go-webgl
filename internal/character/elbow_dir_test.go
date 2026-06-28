package character

import (
	"testing"

	"raytracer/internal/vec"
)

func forearmBendForward(r *Rig, poseName string, forearmPitch float64, side rune) float64 {
	root := vec.New(0, r.HipHeight, 0)
	pose := r.ComputeFK(poseName, root, 0)
	upper, fore := "upper_arm_l", "forearm_l"
	if side == 'r' {
		upper, fore = "upper_arm_r", "forearm_r"
	}
	upperXF := pose.Bones[upper]
	angles := r.PoseAngles(poseName, fore)
	angles.Pitch = forearmPitch
	pose.Bones[fore] = upperXF.ChildAt(r.JointLocal(fore), angles.Pitch, angles.Yaw, angles.Roll)
	elbow := pose.Bones[fore].Translation()
	tip := r.BoneTip(pose, fore)
	fwd := vec.V{Z: -1}
	upperDir := upperXF.RotateDir(vec.V{Y: 1}).Normalize()
	handDir := tip.Sub(elbow).Normalize()
	return handDir.Sub(upperDir).Dot(fwd)
}

func TestForearmPitchBendsForward(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []rune{'l', 'r'} {
		pos := forearmBendForward(r, "walk", 12, side)
		neg := forearmBendForward(r, "walk", -12, side)
		if pos <= neg {
			t.Fatalf("side %c: pitch +12 (%v) should bend more forward than -12 (%v)", side, pos, neg)
		}
	}
}
