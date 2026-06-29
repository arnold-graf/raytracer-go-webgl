package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestClampFootPlanKeepsFeetOnCorrectSides(t *testing.T) {
	hip := vec.V{}
	fwd := yawForward(270)
	right := yawRight(270)
	stride := 0.5

	left := clampFootPlan(hip, hip.Add(vec.V{X: 0.01}), fwd, right, 1, stride)
	lLat := lateralAlongRight(left, hip, right)
	if lLat < minFootLateral {
		t.Fatalf("left lateral=%v want >= %v", lLat, minFootLateral)
	}

	rightFoot := clampFootPlan(hip, hip.Add(vec.V{X: -0.01}), fwd, right, -1, stride)
	rLat := lateralAlongRight(rightFoot, hip, right)
	if rLat > -minFootLateral {
		t.Fatalf("right lateral=%v want <= %v", rLat, -minFootLateral)
	}
	if FeetCrossed(left, rightFoot, hip, 270) {
		t.Fatal("feet should not cross after clamp")
	}
}

func TestClampFootHeightLimitsStepUp(t *testing.T) {
	got := clampFootHeight(1.5, 1.0)
	if math.Abs(got-(1.0+maxFootStepUp)) > 1e-9 {
		t.Fatalf("step up = %v want %v", got, 1.0+maxFootStepUp)
	}
}

func TestEnforceFootRelativeToPartner(t *testing.T) {
	hip := vec.V{}
	right := yawRight(270)
	partner := hip.Add(vec.V{X: -0.14}) // right foot lane
	target := hip.Add(vec.V{X: -0.05})   // wrongly on right side
	fixed := enforceFootRelativeToPartner(target, partner, hip, right, 1)
	lLat := lateralAlongRight(fixed, hip, right)
	rLat := lateralAlongRight(partner, hip, right)
	if lLat-rLat < minFootPairSep-footSeparationMargin {
		t.Fatalf("left=%v partner=%v gap=%v", lLat, rLat, lLat-rLat)
	}
}

func TestWalkFeetStaySeparated(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 1.0, world)
	for i := 0; i < 720; i++ {
		loc.Update(1.0 / 60.0, r, world)
		if !footSeparationOK(loc.Left.World, loc.Right.World, loc.HipPos, loc.Heading) {
			t.Fatalf("frame %d: feet crossed or over-spread L=%v R=%v", i, loc.Left.World, loc.Right.World)
		}
	}
}

func TestWalkKneesDoNotCross(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 1.0, world)
	for i := 0; i < 720; i++ {
		loc.Update(1.0 / 60.0, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		if kneesCrossed(r, pose, loc.HipPos, loc.Heading) {
			t.Fatalf("frame %d: knees crossed", i)
		}
	}
}

func TestWalkFeetStaySeparatedWhileTurning(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 1.0, world)
	for i := 0; i < 720; i++ {
		loc.Heading += 0.35 // ~21°/s turn while walking
		loc.Update(1.0 / 60.0, r, world)
		if !footSeparationOK(loc.Left.World, loc.Right.World, loc.HipPos, loc.Heading) {
			t.Fatalf("frame %d heading=%.1f: feet crossed", i, loc.Heading)
		}
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		if kneesCrossed(r, pose, loc.HipPos, loc.Heading) {
			t.Fatalf("frame %d heading=%.1f: knees crossed", i, loc.Heading)
		}
	}
}

func TestSwingArcStaysInLane(t *testing.T) {
	hip := vec.V{}
	right := yawRight(270)
	from := vec.V{X: -0.14, Z: -0.2}
	to := vec.V{X: 0.14, Z: 0.2} // bad landing on wrong side
	for _, tVal := range []float64{0.25, 0.5, 0.75} {
		p := footArcInLane(from, to, 0.1, tVal, hip, right, 1)
		lat := lateralAlongRight(p, hip, right)
		if lat < minFootLateral-footSeparationMargin {
			t.Fatalf("t=%v arc point lat=%v crossed midline", tVal, lat)
		}
	}
}
