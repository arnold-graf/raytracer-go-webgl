package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

func TestConvexHullSquare(t *testing.T) {
	hull := convexHullXZ([]vec.V{
		{X: -1, Z: -1},
		{X: 1, Z: -1},
		{X: 1, Z: 1},
		{X: -1, Z: 1},
		{X: 0, Z: 0},
	})
	if len(hull) != 4 {
		t.Fatalf("hull vertices = %d, want 4", len(hull))
	}
	if !pointInConvexPolygonXZ(vec.V{}, hull, 0.01) {
		t.Fatal("origin should be inside hull")
	}
}

func TestSpiderSupportPolygonBlocksUnsafeLift(t *testing.T) {
	feet := make([]Foot, 4)
	for i := range feet {
		feet[i] = Foot{
			PlantWorld:  vec.V{X: float64(i), Z: 0},
			Phase:       FootMidStance,
			Initialized: true,
		}
	}
	com := vec.V{X: 10, Z: 10}
	if spiderCanLift(feet, 0, com, 0.1) {
		t.Fatal("should not lift when COM is far outside support")
	}
}

func TestSpiderIKSmoothsBetweenFrames(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	knee0 := s.Feet[0].Solve.Knee
	for i := 0; i < 5; i++ {
		s.Update(1.0/60.0, r, world)
	}
	knee1 := s.Feet[0].Solve.Knee
	jump := knee1.Sub(knee0).Len()
	if jump > 0.5 {
		t.Fatalf("knee jumped %.3f in 5 frames", jump)
	}
}

func TestSpiderStaticStanceFeetOnGround(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0, world)
	for i := 0; i < 120; i++ {
		s.Update(1.0/60.0, r, world)
	}
	pose := s.ComputePose(r)
	for i, leg := range r.LegDefs() {
		tip := r.BoneTip(pose, leg.Distal)
		if tip.Y > 0.15 {
			t.Fatalf("leg %s tip Y=%.3f above ground", leg.Prefix, tip.Y)
		}
		if i < len(s.Feet) && math.Abs(tip.Y-s.Feet[i].World.Y) > 0.45 {
			t.Fatalf("leg %s tip Y=%.3f far from foot target %.3f", leg.Prefix, tip.Y, s.Feet[i].World.Y)
		}
	}
}

func TestSpiderWalkAdvances(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	z0 := s.Body.Pos.Z
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
	}
	if s.Body.Pos.Z >= z0-0.3 {
		t.Fatalf("body should advance along -Z, z0=%.3f now=%.3f", z0, s.Body.Pos.Z)
	}
}

func TestSpiderFastWalkKeepsPace(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 2.0, world)
	z0 := s.Body.Pos.Z
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
	}
	dist := z0 - s.Body.Pos.Z
	// Nominal 8m in 4s; allow leg lag and startup.
	if dist < 2.0 {
		t.Fatalf("speed 2: moved only %.2fm in 4s", dist)
	}
}

func TestSpiderCoxaPointsRadially(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	fwd := yawForward(0)
	for i := 0; i < 180; i++ {
		s.Update(1.0/60.0, r, world)
	}
	bodyXf := s.Body.Transform()
	hip := bodyXf.Translation()
	for i, leg := range r.LegDefs() {
		if i >= len(s.Feet) || !s.Feet[i].Solve.Valid {
			continue
		}
		ik := s.Feet[i].Solve
		rest := hipWorldOffset(hip, ik.HipSocket.Y, 0, leg.RestOffset)
		radial := legRadialDir(hip, rest, 0)
		coxa := ik.CoxaTip.Sub(ik.HipSocket)
		if coxa.LenSq() < 1e-12 || radial.LenSq() < 1e-12 {
			continue
		}
		coxaH := vec.V{X: coxa.X, Z: coxa.Z}
		if coxaH.LenSq() < 1e-12 {
			t.Fatalf("%s coxa has no horizontal aim", leg.Prefix)
		}
		coxaH = coxaH.Normalize()
		dot := coxaH.X*radial.X + coxaH.Z*radial.Z
		if dot < 0.75 {
			t.Fatalf("%s coxa not radial: dot=%.2f", leg.Prefix, dot)
		}
		fwdDot := coxaH.X*fwd.X + coxaH.Z*fwd.Z
		restZ := leg.RestOffset.Z
		if restZ < -0.15 && fwdDot < 0.05 {
			t.Fatalf("%s front leg coxa not forward: fwdDot=%.2f", leg.Prefix, fwdDot)
		}
		if restZ > 0.15 && fwdDot > -0.05 {
			t.Fatalf("%s rear leg coxa not backward: fwdDot=%.2f", leg.Prefix, fwdDot)
		}
	}
}

func TestSpiderCoxaRadialStaysStable(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 2.0, world)

	measure := func() map[string]float64 {
		out := make(map[string]float64)
		bodyXf := s.Body.Transform()
		hip := bodyXf.Translation()
		for i, leg := range r.LegDefs() {
			if i >= len(s.Feet) || !s.Feet[i].Solve.Valid {
				continue
			}
			ik := s.Feet[i].Solve
			rest := hipWorldOffset(hip, ik.HipSocket.Y, 0, leg.RestOffset)
			radial := legRadialDir(hip, rest, 0)
			horiz := vec.V{X: ik.CoxaTip.X - ik.HipSocket.X, Z: ik.CoxaTip.Z - ik.HipSocket.Z}
			if horiz.LenSq() < 1e-12 || radial.LenSq() < 1e-12 {
				continue
			}
			horiz = horiz.Normalize()
			out[leg.Prefix] = horiz.X*radial.X + horiz.Z*radial.Z
		}
		return out
	}

	for i := 0; i < 120; i++ {
		s.Update(1.0/60.0, r, world)
	}
	base := measure()
	for i := 0; i < 480; i++ {
		s.Update(1.0/60.0, r, world)
	}
	late := measure()
	for prefix, b := range base {
		l, ok := late[prefix]
		if !ok {
			continue
		}
		if l < b-0.12 {
			t.Fatalf("%s coxa radial alignment dropped: %.2f -> %.2f", prefix, b, l)
		}
		if l < 0.65 {
			t.Fatalf("%s coxa not radial after walk: dot=%.2f", prefix, l)
		}
	}
}

func TestLegSolveJointsConnect(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0, world)
	s.Update(1.0/60.0, r, world)
	pose := s.ComputePose(r)
	for _, leg := range r.LegDefs() {
		gap := r.BoneTip(pose, leg.Proximal).Sub(pose.Bones[leg.Mid].Translation()).Len()
		if gap > 0.08 {
			t.Fatalf("%s coxa-femur gap = %.3f", leg.Prefix, gap)
		}
	}
}

func TestSpiderHipPlantDistanceBounded(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	for i := 0; i < 60; i++ {
		s.Update(1.0/60.0, r, world)
	}
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
		if d := s.maxHipPlantHoriz(r); d > spiderMaxHipPlantHoriz+0.02 {
			t.Fatalf("frame %d: hip-plant %.3f > %.3f", i+60, d, spiderMaxHipPlantHoriz)
		}
	}
}

func TestSpiderSpeedFiveMoves(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 5.0, world)
	z0 := s.Body.Pos.Z
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
	}
	dist := z0 - s.Body.Pos.Z
	if dist < 5.5 {
		t.Fatalf("speed 5: moved only %.2fm in 4s", dist)
	}
}

func TestSpiderAtMostThreeSwinging(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	pace := s.stepPace(r, s.gaitStride(r))
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
		swinging := 0
		for _, f := range s.Feet {
			if f.Phase == FootSwing {
				swinging++
			}
		}
		if swinging > pace.maxSwing {
			t.Fatalf("frame %d: %d legs swinging > %d", i, swinging, pace.maxSwing)
		}
	}
}

func TestSpiderHighSpeedSwingCap(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 5.0, world)
	pace := s.stepPace(r, s.gaitStride(r))
	if pace.maxSwing <= spiderMaxConcurrentSwing {
		t.Fatalf("high speed maxSwing = %d, want > %d", pace.maxSwing, spiderMaxConcurrentSwing)
	}
	for i := 0; i < 240; i++ {
		s.Update(1.0/60.0, r, world)
		swinging := 0
		for _, f := range s.Feet {
			if f.Phase == FootSwing {
				swinging++
			}
		}
		if swinging > pace.maxSwing {
			t.Fatalf("frame %d: %d legs swinging > %d", i, swinging, pace.maxSwing)
		}
	}
}

func TestSpiderJointMotionIsContinuous(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, world)
	var prev vec.V
	for i := 0; i < 180; i++ {
		s.Update(1.0/60.0, r, world)
		knee := s.Feet[0].Solve.Knee
		if i > 0 {
			delta := knee.Sub(prev).Len()
			if delta > 0.15 {
				t.Fatalf("frame %d: knee moved %.3f in one tick", i, delta)
			}
		}
		prev = knee
	}
}

func TestSpiderLegPoseUsesFootContacts(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0.85, flatGround{})
	s.Feet[0].Initialized = true
	s.Feet[0].Phase = FootMidStance
	s.Feet[0].PlantWorld = vec.V{Y: 0}
	s.Feet[0].Solve.Valid = true
	s.Feet[0].Solve.Foot = vec.V{Y: 0.2}
	if got := s.footContactPoint(0); math.Abs(got.Y-0.2) > 1e-6 {
		t.Fatalf("footContactPoint = %v, want IK foot", got)
	}
	s.Feet[0].Phase = FootSwing
	s.Feet[0].World = vec.V{Y: 0.35}
	if got := s.footContactPoint(0); math.Abs(got.Y-0.35) > 1e-6 {
		t.Fatalf("swing footContactPoint = %v, want swing world", got)
	}
}

func TestSpiderSagittalNormalSuppressesRoll(t *testing.T) {
	n := vec.V{Y: 0.8, X: 0.3, Z: 0.2}.Normalize()
	flat := spiderSagittalNormal(n, 0)
	if math.Abs(flat.X) > 0.05 {
		t.Fatalf("sagittal normal should remove lateral X, got %v", flat)
	}
}

func TestSpiderPlantedFootForcesStable(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.5, world)
	for i := 0; i < 300; i++ {
		s.Update(1.0/60.0, r, world)
		if math.IsNaN(s.Body.Vel.X) || math.IsNaN(s.Body.Vel.Y) || math.IsNaN(s.Body.Vel.Z) {
			t.Fatalf("frame %d: NaN velocity", i)
		}
		if s.Body.Pos.Y > 3 || s.Body.Pos.Y < -0.5 {
			t.Fatalf("frame %d: body Y=%.3f out of range", i, s.Body.Pos.Y)
		}
	}
}
