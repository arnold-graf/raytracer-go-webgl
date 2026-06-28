package character

import (
	"testing"

	"raytracer/internal/vec"
)

func TestFootPlantOnGround(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0, world)
	for i := 0; i < 30; i++ {
		loc.Update(1.0/60.0, r, world)
	}
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
