package character

import (
	"math"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func TestGaitStateForSpeed(t *testing.T) {
	if GaitStateForSpeed(0) != GaitIdle {
		t.Fatal("zero speed should idle")
	}
	if GaitStateForSpeed(1.4) != GaitWalk {
		t.Fatal("walk speed")
	}
	if GaitStateForSpeed(4) != GaitRun {
		t.Fatal("run speed")
	}
}

func TestFootArc(t *testing.T) {
	from := vec.New(0, 0, 0)
	to := vec.New(1, 0, 0)
	mid := footArcBezier(from, to, 0.2, 0.5)
	if mid.Y < 0.05 {
		t.Fatalf("arc midpoint should lift, got Y=%v", mid.Y)
	}
	end := footArcBezier(from, to, 0.2, 1)
	if math.Abs(end.X-1) > 1e-9 || math.Abs(end.Z) > 1e-9 {
		t.Fatalf("arc end = %v, want (1,0,0)", end)
	}
}

func TestLocomotorUpdate(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)
	z0 := loc.HipPos.Z
	for i := 0; i < 60; i++ {
		loc.Update(0.1, r, world)
	}
	if loc.HipPos.Z >= z0 {
		t.Fatalf("hip should advance along -Z at heading 0, z0=%v now=%v", z0, loc.HipPos.Z)
	}
	if !loc.Left.Initialized || !loc.Right.Initialized {
		t.Fatal("feet should be initialized")
	}
}

func TestLocomotorFeetStayOnCorrectSides(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)
	for i := 0; i < 120; i++ {
		loc.Update(1.0 / 60.0, r, world)
		if loc.Left.World.X <= loc.Right.World.X {
			t.Fatalf("left foot X=%v should stay right of right foot X=%v at frame %d",
				loc.Left.World.X, loc.Right.World.X, i)
		}
	}
}

func TestLocomotionKneeBendsDuringStride(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)
	maxFlex := 0.0
	for i := 0; i < 180; i++ {
		loc.Update(1.0 / 60.0, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		for _, pair := range []struct {
			thigh, shin string
		}{
			{"thigh_l", "shin_l"},
			{"thigh_r", "shin_r"},
		} {
			hip := pose.Bones["hips"].ToWorld(r.JointLocal(pair.thigh))
			shin := pose.Bones[pair.shin]
			if shin == nil {
				continue
			}
			knee := shin.Translation()
			ankle := r.BoneTip(pose, pair.shin)
			flex := kneeFlex(hip, knee, ankle)
			if flex > maxFlex {
				maxFlex = flex
			}
		}
	}
	// 2 = fully straight; 22° min bend caps flex around 1.93.
	if maxFlex > 1+math.Cos(minKneeBendDeg*math.Pi/180)+0.02 {
		t.Fatalf("knee over-extended during walk, max flex=%v (2=straight)", maxFlex)
	}
}

func TestComputeLocomotionPoseIK(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 1.2, world)
	loc.Update(0.1, r, world)
	pose := ComputeLocomotionPose(r, &loc, "idle", world)
	if pose.Bones["thigh_l"] == nil || pose.Bones["shin_l"] == nil || pose.Bones["foot_l"] == nil {
		t.Fatal("missing leg bones in locomotion pose")
	}
	thighTip := r.BoneTip(pose, "thigh_l")
	knee := pose.Bones["shin_l"].Translation()
	if thighTip.Sub(knee).Len() > 0.03 {
		t.Fatalf("knee gap: thigh tip %v vs shin base %v", thighTip, knee)
	}
}

type flatGround struct{}

func (flatGround) GroundHeight(x, z, headY float64) float64 { return 0 }
func (flatGround) GroundNormal(x, z, headY float64) vec.V    { return vec.V{Y: 1} }

func TestSceneGroundNormal(t *testing.T) {
	sc := &scene.Scene{
		Boxes: []scene.Box{{
			Min:     vec.New(0, 0, 0),
			Max:     vec.New(10, 0.2, 10),
			Surface: scene.Surface{Mat: scene.MatDiffuse},
		}},
	}
	n := sc.GroundNormal(5, 5, 2)
	if n.Y < 0.9 {
		t.Fatalf("flat box normal Y=%v, want ~1", n.Y)
	}
}
