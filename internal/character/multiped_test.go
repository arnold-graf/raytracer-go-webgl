package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestSpiderLegDefs(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	legs := r.LegDefs()
	if len(legs) != 8 {
		t.Fatalf("legs = %d, want 8", len(legs))
	}
	for _, leg := range legs {
		if leg.Kind != LegKindTripod {
			t.Fatalf("%s: kind = %v, want tripod", leg.Prefix, leg.Kind)
		}
	}
}

func TestSpiderWaveGaitOneSwingAtATime(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Locomotion.SwingFraction > 0.2 {
		t.Fatalf("swing_fraction = %.2f, want <= 0.2 for wave gait", r.Locomotion.SwingFraction)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0.7, world)
	for i := 0; i < 240; i++ {
		loc.Update(1.0/60.0, r, world)
		swinging := 0
		for _, f := range loc.Feet {
			if f.Initialized && f.Phase == FootSwing {
				swinging++
			}
		}
		if swinging > 2 {
			t.Fatalf("frame %d: %d legs swinging, want at most 2", i, swinging)
		}
	}
}

func TestSpiderLocomotionAdvances(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0.7, world)
	z0 := loc.HipPos.Z
	for i := 0; i < 120; i++ {
		loc.Update(1.0 / 60.0, r, world)
	}
	if loc.HipPos.Z >= z0 {
		t.Fatalf("hip should advance along -Z, z0=%v now=%v", z0, loc.HipPos.Z)
	}
}

func TestSpiderLocomotionPoseIK(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0.7, world)
	for i := 0; i < 30; i++ {
		loc.Update(1.0 / 60.0, r, world)
	}
	pose := ComputeLocomotionPose(r, &loc, "idle", world)
	for _, prefix := range []string{"fl", "fr", "ml", "mr"} {
		coxa := prefix + "_coxa"
		femur := prefix + "_femur"
		tibia := prefix + "_tibia"
		if pose.Bones[coxa] == nil || pose.Bones[femur] == nil || pose.Bones[tibia] == nil {
			t.Fatalf("missing bones for %s", prefix)
		}
		gap := r.BoneTip(pose, coxa).Sub(pose.Bones[femur].Translation()).Len()
		if gap > 0.05 {
			t.Fatalf("%s coxa-femur gap = %.3f", prefix, gap)
		}
	}
}

func TestSpiderCoxaFlexesDuringWalk(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0.85, world)
	idleCoxa := r.ComputeFK("idle", loc.HipPos, loc.Heading)

	flexed := false
	for i := 0; i < 120; i++ {
		loc.Update(1.0 / 60.0, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		for _, prefix := range []string{"fl", "ml", "rl"} {
			coxa := prefix + "_coxa"
			idleXF := idleCoxa.Bones[coxa]
			walkXF := pose.Bones[coxa]
			if idleXF == nil || walkXF == nil {
				continue
			}
			idleTip := r.BoneTip(idleCoxa, coxa)
			walkTip := r.BoneTip(pose, coxa)
			if idleTip.Sub(walkTip).Len() > 0.08 {
				flexed = true
			}
		}
	}
	if !flexed {
		t.Fatal("coxa joints did not move during walk")
	}
}

func TestCoxaDirNeverPointsBackward(t *testing.T) {
	radial := vec.V{X: -0.6, Z: 0.4}.Normalize()
	fwd := vec.V{Z: -1}
	tooBack := vec.V{X: 0.5, Y: 0.5, Z: 0.8}.Normalize()
	clamped, ok := clampCoxaDir(tooBack, radial, fwd, 0.3)
	if !ok {
		t.Fatal("expected clamped direction")
	}
	horiz := vec.V{X: clamped.X, Z: clamped.Z}
	hl := horiz.Len()
	if hl < 1e-9 {
		t.Fatal("expected horizontal component")
	}
	fwdDot := (horiz.X*fwd.X + horiz.Z*fwd.Z) / hl
	if fwdDot < -0.05 {
		t.Fatalf("clamped horiz fwdDot = %.3f, want >= 0", fwdDot)
	}
}

func TestSpiderWalkingTipsReachGround(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	for i := 0; i < 90; i++ {
		s.Update(1.0/60.0, r, world)
		pose := s.ComputePose(r)
		if i == 0 {
			continue
		}
		for j, leg := range r.LegDefs() {
			foot := s.Feet[j]
			tip := r.BoneTip(pose, leg.Distal)
			if foot.Phase == FootSwing {
				if math.Abs(tip.Y-foot.World.Y) > 0.35 {
					t.Fatalf("frame %d %s swing tip Y=%.3f foot Y=%.3f", i, leg.Prefix, tip.Y, foot.World.Y)
				}
				continue
			}
			if math.Abs(tip.Y-foot.World.Y) > 0.35 {
				t.Fatalf("frame %d %s stance tip Y=%.3f foot Y=%.3f", i, leg.Prefix, tip.Y, foot.World.Y)
			}
			if tip.Y > 0.35 {
				t.Fatalf("frame %d %s stance tip Y=%.3f above ground", i, leg.Prefix, tip.Y)
			}
		}
	}
}

func TestTripodLegIKStableForSameTarget(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	leg := r.LegDefs()[0]
	coxa := r.Bones[leg.Proximal]
	femur := r.Bones[leg.Mid]
	tibia := r.Bones[leg.Distal]

	root := vec.V{X: -0.56, Y: 0.68, Z: 0.45}
	target := vec.V{X: -0.56, Y: 0.07, Z: 0.53}
	radial := vec.V{X: -0.4, Y: 0, Z: 0.2}

	first := solveBallCoxaLeg(root, target, radial, vec.V{X: 0, Z: -1}, vec.V{X: 1}, 1, 0.30, nil, coxa.Length, femur.Length, tibia.Length, 30)
	if !first.ok {
		t.Fatal("first solve failed")
	}
	foot := &Foot{Solve: LegSolve{Valid: true, CoxaTip: first.j1, Knee: first.j2, Foot: first.end}}
	second := solveBallCoxaLeg(root, target, radial, vec.V{X: 0, Z: -1}, vec.V{X: 1}, 1, 0.30, foot, coxa.Length, femur.Length, tibia.Length, 30)
	if !second.ok {
		t.Fatal("second solve failed")
	}
	if first.j1.Sub(second.j1).Len() > 0.02 {
		t.Fatalf("coxa tip moved %.3f for unchanged target", first.j1.Sub(second.j1).Len())
	}
}

func TestSpiderBodyBalance(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 0, 0.7, world)
	for i := 0; i < 180; i++ {
		loc.Update(1.0 / 60.0, r, world)
	}
	if math.Abs(loc.BodyPitch) > 12 || math.Abs(loc.BodyRoll) > 10 {
		t.Fatalf("body tilt excessive: pitch=%.1f roll=%.1f", loc.BodyPitch, loc.BodyRoll)
	}
}
