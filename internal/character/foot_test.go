package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestFootPlantOnGround(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)
	loc.Update(0.1, r, world)
	pose := ComputeLocomotionPose(r, &loc, "idle", world)

	for _, foot := range []string{"foot_l", "foot_r"} {
		if pose.Bones[foot] == nil {
			t.Fatalf("missing %s", foot)
		}
		sole := footSoleWorld(r, pose, foot)
		if sole.Y > 0.04 {
			t.Fatalf("%s sole Y=%v, want near ground", foot, sole.Y)
		}
		ankle := pose.Bones[foot].Translation()
		if ankle.Y < sole.Y {
			t.Fatalf("%s ankle Y=%v should be above sole Y=%v", foot, ankle.Y, sole.Y)
		}
	}
}

func TestIdleFeetPlanted(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0, world)
	pose := ComputeLocomotionPose(r, &loc, "idle", world)
	for _, foot := range []string{"foot_l", "foot_r"} {
		sole := footSoleWorld(r, pose, foot)
		if sole.Y > 0.04 {
			t.Fatalf("idle %s sole Y=%v, want near ground", foot, sole.Y)
		}
	}
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
