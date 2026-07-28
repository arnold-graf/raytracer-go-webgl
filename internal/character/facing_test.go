package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestHeadingZeroMovesAndFacesNegZ(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)
	z0 := loc.HipPos.Z
	for i := 0; i < 30; i++ {
		loc.Update(1.0 / 60.0, r, world)
	}
	fwd := yawForward(0)
	if loc.HipPos.Z >= z0 {
		t.Fatalf("hip should move along %v, z0=%v now=%v", fwd, z0, loc.HipPos.Z)
	}

	pose := ComputeLocomotionPose(r, &loc, "idle", world)
	hips := pose.Bones["hips"]
	chestFwd := hips.RotateDir(vec.V{Z: -1}) // rig front is local -Z
	if chestFwd.Dot(fwd) < 0.9 {
		t.Fatalf("chest forward %v should align with travel %v", chestFwd, fwd)
	}
}

func TestFootToePointsForward(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)
	for i := 0; i < 480; i++ {
		loc.Update(1.0 / 60.0, r, world)
	}
	pose := ComputeLocomotionPose(r, &loc, "idle", world)
	travel := yawForward(loc.Heading)

	for _, side := range []struct {
		name string
		foot Foot
	}{
		{"foot_l", loc.Left},
		{"foot_r", loc.Right},
	} {
		foot := side.name
		f := side.foot
		xf := pose.Bones[foot]
		if xf == nil {
			t.Fatalf("missing %s", foot)
		}
		boneFwd := xf.RotateDir(vec.V{Y: 1})
		if boneFwd.Dot(travel) < 0.5 {
			t.Fatalf("%s bone +Y %v should point roughly along travel %v", foot, boneFwd, travel)
		}
		ankle := xf.Translation()
		toe := footToeWorld(r, pose, foot)
		if toe.Sub(ankle).Dot(travel) < 0.01 {
			t.Fatalf("%s shoe toe %v should be ahead of ankle %v along travel %v", foot, toe, ankle, travel)
		}
		if math.Abs(footRollDeg(f.Phase, f.StanceT, r.Locomotion.FootRoll)) > 1 {
			continue // roll temporarily shifts heel/toe along travel
		}
		heel := footHeelWorld(r, pose, foot)
		if heel.Sub(ankle).Dot(travel) > 0 {
			t.Fatalf("%s shoe heel %v should be behind ankle %v along travel %v", foot, heel, ankle, travel)
		}
	}
}

func footToeWorld(r *Rig, pose SkeletonPose, footName string) vec.V {
	att := footAttachment(r, footName)
	if att == nil || pose.Bones[footName] == nil {
		return vec.V{}
	}
	xf := pose.Bones[footName]
	return xf.ToWorld(att.Offset.Add(vec.V{Y: att.Size.Y * 0.5}))
}

func footHeelWorld(r *Rig, pose SkeletonPose, footName string) vec.V {
	att := footAttachment(r, footName)
	if att == nil || pose.Bones[footName] == nil {
		return vec.V{}
	}
	xf := pose.Bones[footName]
	return xf.ToWorld(att.Offset.Sub(vec.V{Y: att.Size.Y * 0.5}))
}
