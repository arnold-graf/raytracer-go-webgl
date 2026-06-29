package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

type steppedGround struct {
	run  float64
	rise float64
	base float64
}

func (s steppedGround) GroundHeight(x, z, headY float64) float64 {
	if x < s.base {
		return 0
	}
	step := math.Floor((x - s.base) / s.run)
	return (step + 1) * s.rise
}

func (s steppedGround) GroundNormal(x, z, headY float64) vec.V {
	return vec.V{Y: 1}
}

// variedStepsGround models arbitrary stair-like geometry with non-uniform rises/runs.
type variedStepsGround struct {
	baseX float64
	runs  []float64
	rises []float64
}

func (g variedStepsGround) GroundHeight(x, z, headY float64) float64 {
	if x < g.baseX || len(g.runs) == 0 || len(g.rises) == 0 {
		return 0
	}
	h := 0.0
	cursor := g.baseX
	n := len(g.runs)
	if len(g.rises) < n {
		n = len(g.rises)
	}
	for i := 0; i < n; i++ {
		h += g.rises[i]
		if x < cursor+g.runs[i] {
			return h
		}
		cursor += g.runs[i]
	}
	return h
}

func (variedStepsGround) GroundNormal(x, z, headY float64) vec.V {
	return vec.V{Y: 1}
}

// steppedGroundWithEdgeNormals mimics real scene finite-difference normals:
// tread centres return Y≈1, step edges return a mixed normal Y≈0.54.
type steppedGroundWithEdgeNormals struct {
	steppedGround
}

func (s steppedGroundWithEdgeNormals) GroundNormal(x, z, headY float64) vec.V {
	const eps = 0.08
	hL := s.GroundHeight(x-eps, z, headY)
	hR := s.GroundHeight(x+eps, z, headY)
	hN := s.GroundHeight(x, z-eps, headY)
	hS := s.GroundHeight(x, z+eps, headY)
	n := vec.V{X: hL - hR, Y: 2 * eps, Z: hN - hS}
	if n.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	return n.Scale(1.0 / math.Sqrt(n.LenSq()))
}

// TestStairsWithRealisticEdgeNormals guards against the regression where
// finite-difference normals at step edges (~0.54 Y) caused foot targets to be
// rejected, leaving feet on lower treads and the body walking through stairs.
func TestStairsWithRealisticEdgeNormals(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := steppedGroundWithEdgeNormals{steppedGround{run: 0.4, rise: 0.25, base: 2.0}}
	loc := NewLocomotor(r, vec.V{X: -1, Z: 0}, 270, 1.0, world)
	for i := 0; i < 360; i++ {
		loc.Update(1.0/60.0, r, world)
	}
	// After traversing 6+ steps the pelvis must be near the expected tread height.
	expected := world.GroundHeight(loc.HipPos.X, 0, 99)
	if loc.GroundY < expected-0.35 {
		t.Fatalf("hip ground lag with realistic normals: gy=%.2f expected≈%.2f hipX=%.2f",
			loc.GroundY, expected, loc.HipPos.X)
	}
}

func TestFootStepHeightClampedOnStairs(t *testing.T) {
	if got := clampFootHeight(1.0, 0.25); got != 0.75 {
		t.Fatalf("clamp up = %v want 0.75", got)
	}
	if got := clampFootHeight(0.5, 0.25); got != 0.5 {
		t.Fatalf("clamp within limit = %v want 0.5", got)
	}
}

func TestSwingLiftIncreasesOnStepUp(t *testing.T) {
	base := 0.08
	flat := swingLift(vec.V{Y: 0.0}, vec.V{Y: 0.0}, base)
	up := swingLift(vec.V{Y: 0.0}, vec.V{Y: 0.25}, base)
	if flat != base {
		t.Fatalf("flat swing lift = %.3f want %.3f", flat, base)
	}
	if up <= flat+0.12 {
		t.Fatalf("step-up swing lift too small: flat=%.3f up=%.3f", flat, up)
	}
}

func TestStairsHaveVisibleKneeFlex(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := steppedGround{run: 0.4, rise: 0.25, base: 2.0}
	loc := NewLocomotor(r, vec.V{X: 1.5, Z: 0}, 270, 1.0, world)
	maxFlex := 0.0
	for i := 0; i < 360; i++ {
		loc.Update(1.0 / 60.0, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		for _, pair := range []struct{ thigh, shin string }{
			{"thigh_l", "shin_l"},
			{"thigh_r", "shin_r"},
		} {
			flex := kneeFlexDeg(r, pose, pair.thigh, pair.shin)
			if flex > maxFlex {
				maxFlex = flex
			}
		}
	}
	if maxFlex < 20.0 {
		t.Fatalf("knee flex on stairs too small: max=%.1f° want >= 20°", maxFlex)
	}
}

// TestStairsRepeatedKneeFlexNotHipShift guards against climbing by Y-snapping the pelvis
// to each tread (straight legs after the first step). Each swing cycle on stairs should
// show meaningful knee flex, not just the first footfall.
func TestStairsRepeatedKneeFlexNotHipShift(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := steppedGround{run: 0.4, rise: 0.25, base: 2.0}
	loc := NewLocomotor(r, vec.V{X: -1, Z: 0}, 270, 1.0, world)
	const dt = 1.0 / 60.0

	var swingPeaks []float64
	var peakFlex float64
	wasSwinging := false
	prevGy := loc.GroundY

	for i := 0; i < 480; i++ {
		loc.Update(dt, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)

		swinging := loc.Left.Phase == FootSwing || loc.Right.Phase == FootSwing
		frameFlex := 0.0
		for _, pair := range []struct{ thigh, shin string }{
			{"thigh_l", "shin_l"},
			{"thigh_r", "shin_r"},
		} {
			flex := kneeFlexDeg(r, pose, pair.thigh, pair.shin)
			if flex > frameFlex {
				frameFlex = flex
			}
		}

		if swinging {
			if frameFlex > peakFlex {
				peakFlex = frameFlex
			}
		} else if wasSwinging {
			swingPeaks = append(swingPeaks, peakFlex)
			peakFlex = 0
		}
		wasSwinging = swinging

		// Large GroundY jumps mid-stride (not near landing) mean the body teleported up.
		gyJump := loc.GroundY - prevGy
		if gyJump > 0.10 {
			lateSwing := (loc.Left.Phase == FootSwing && loc.Left.SwingT >= swingHipPreviewStart) ||
				(loc.Right.Phase == FootSwing && loc.Right.SwingT >= swingHipPreviewStart)
			justLanded := (loc.Left.Phase != FootSwing && loc.Left.StanceT < 0.08) ||
				(loc.Right.Phase != FootSwing && loc.Right.StanceT < 0.08)
			if !lateSwing && !justLanded {
				t.Fatalf("frame %d: hip ground jumped %.3f without swing/landing (gy=%.2f flex=%.1f°)",
					i, gyJump, loc.GroundY, frameFlex)
			}
		}
		prevGy = loc.GroundY
	}
	if wasSwinging && peakFlex > 0 {
		swingPeaks = append(swingPeaks, peakFlex)
	}

	if len(swingPeaks) < 4 {
		t.Fatalf("expected multiple stair swing cycles, got %d", len(swingPeaks))
	}
	flexingSwings := 0
	for _, p := range swingPeaks {
		if p >= 18.0 {
			flexingSwings++
		}
	}
	if flexingSwings < 4 {
		t.Fatalf("only %d/%d swings flexed knees >= 18° (peaks=%v)", flexingSwings, len(swingPeaks), swingPeaks)
	}
}

func TestHipTracksStairsAtWalkSpeed(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := steppedGround{run: 0.4, rise: 0.25, base: 2.0}
	loc := NewLocomotor(r, vec.V{X: -1, Z: 0}, 270, 1.0, world)
	for i := 0; i < 420; i++ {
		loc.Update(1.0 / 60.0, r, world)
	}
	expected := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, loc.HipPos.Y+groundHeadClearance)
	if loc.GroundY < expected-0.35 {
		t.Fatalf("hip ground lag: got=%.2f expected≈%.2f hipX=%.2f", loc.GroundY, expected, loc.HipPos.X)
	}
	if loc.HipPos.Y < r.HipHeight+expected-0.35 {
		t.Fatalf("hip Y=%.2f too low for surface %.2f at x=%.2f", loc.HipPos.Y, expected, loc.HipPos.X)
	}
}

func TestHipTerrainLagBoundedOnVariedSteps(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := variedStepsGround{
		baseX: 2.0,
		runs:  []float64{0.34, 0.51, 0.37, 0.45, 0.29, 0.42, 0.38},
		rises: []float64{0.18, 0.24, 0.16, 0.27, 0.14, 0.21, 0.19},
	}
	loc := NewLocomotor(r, vec.V{X: -1, Z: 0}, 270, 1.0, world)
	maxLag := 0.0
	for i := 0; i < 540; i++ {
		loc.Update(1.0/60.0, r, world)
		terrain := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, loc.HipPos.Y+groundHeadClearance)
		lag := terrain - loc.GroundY
		if lag > maxLag {
			maxLag = lag
		}
	}
	// Keep pelvis close enough to terrain under it to avoid visible clipping on generic steps.
	if maxLag > 0.26 {
		t.Fatalf("pelvis terrain lag too large on varied steps: max=%.3f", maxLag)
	}
}

func TestUpperStepSwingsKeepKneeFlex(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := steppedGround{run: 0.4, rise: 0.25, base: 2.0}
	loc := NewLocomotor(r, vec.V{X: -1, Z: 0}, 270, 1.0, world)
	const dt = 1.0 / 60.0

	type swingTrack struct {
		wasSwing bool
		stepUp   bool
		minFlex  float64 // kneeFlexDeg: lower = more bent
	}
	tracks := [2]swingTrack{{minFlex: 180}, {minFlex: 180}}
	upperStepPeaks := []float64{}

	for i := 0; i < 600; i++ {
		loc.Update(dt, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)

		feet := []Foot{loc.Left, loc.Right}
		for idx, f := range feet {
			swinging := f.Phase == FootSwing
			if swinging {
				if !tracks[idx].wasSwing {
					tracks[idx].stepUp = f.SwingTo.Y-f.PlantGroundY > stepUpMinHeight && f.SwingTo.Y >= 1.0
					tracks[idx].minFlex = 180
				}
				var flex float64
				if idx == 0 {
					flex = kneeFlexDeg(r, pose, "thigh_l", "shin_l")
				} else {
					flex = kneeFlexDeg(r, pose, "thigh_r", "shin_r")
				}
				if flex < tracks[idx].minFlex {
					tracks[idx].minFlex = flex
				}
			} else if tracks[idx].wasSwing && tracks[idx].stepUp {
				upperStepPeaks = append(upperStepPeaks, tracks[idx].minFlex)
			}
			tracks[idx].wasSwing = swinging
		}
	}

	if len(upperStepPeaks) < 2 {
		t.Fatalf("expected upper-step swing samples, got %d", len(upperStepPeaks))
	}
	for i, p := range upperStepPeaks {
		// kneeFlexDeg <= 155 means at least ~25° of visible knee bend during the swing.
		if p > 155.0 {
			t.Fatalf("upper-step swing %d too straight: min flex=%.1f° (all=%v)", i, p, upperStepPeaks)
		}
	}
}

func TestStairsPelvisRiseIsSmooth(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := steppedGround{run: 0.4, rise: 0.25, base: 2.0}
	loc := NewLocomotor(r, vec.V{X: -1, Z: 0}, 270, 1.0, world)

	prevGy := loc.GroundY
	maxJump := 0.0
	for i := 0; i < 420; i++ {
		loc.Update(1.0/60.0, r, world)
		dy := loc.GroundY - prevGy
		if dy > maxJump {
			maxJump = dy
		}
		prevGy = loc.GroundY
	}

	// A full-tread (0.25m) instant snap is visibly abrupt. Keep per-frame rise bounded.
	if maxJump > 0.08 {
		t.Fatalf("pelvis rise too abrupt on stairs: max frame jump=%.3f", maxJump)
	}
}
