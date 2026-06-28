package character

import (
	"math"
	"strings"
	"testing"

	"raytracer/internal/vec"
)

func TestWalkDoesNotMoonwalk(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 0.2, world)
	var recs []PoseRecord
	for frame := 0; frame < 480; frame++ {
		loc.Update(1.0/60.0, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		recs = append(recs, BuildPoseRecord(frame, "test", r, &loc, pose, world))
	}
	rep := AnalyzePoseRecords(recs)
	if rep.StanceBackwardSkate > 5 {
		t.Fatalf("stance backward skate=%d, report:\n%s", rep.StanceBackwardSkate, FormatGaitReport(rep))
	}
}

func TestSwingFootMovesForwardOnAverage(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 0.2, world)
	fwd := yawForward(loc.Heading)
	swingFwd := 0
	swingBack := 0
	var prev PoseRecord
	for frame := 0; frame < 480; frame++ {
		loc.Update(1.0/60.0, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		rec := BuildPoseRecord(frame, "test", r, &loc, pose, world)
		if frame > 0 {
			for _, side := range []struct {
				phase string
				prevW [3]float64
				curW  [3]float64
			}{
				{rec.Left.Phase, prev.Left.World, rec.Left.World},
				{rec.Right.Phase, prev.Right.World, rec.Right.World},
			} {
				if side.phase != "swing" {
					continue
				}
				vel := vecFromArray(side.curW).Sub(vecFromArray(side.prevW)).Dot(fwd)
				if vel > 0.0001 {
					swingFwd++
				} else if vel < -0.0001 {
					swingBack++
				}
			}
		}
		prev = rec
	}
	if swingBack > swingFwd/4 {
		t.Fatalf("swing foot often moves backward: fwd=%d back=%d", swingFwd, swingBack)
	}
}
func TestFeetPassInFrontOfTorso(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/humanoid.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	loc := NewLocomotor(r, vec.V{}, 270, 1.0, world)
	maxAhead := math.Inf(-1)
	for i := 0; i < 480; i++ {
		loc.Update(1.0/60.0, r, world)
		pose := ComputeLocomotionPose(r, &loc, "idle", world)
		m := ComputeGaitMetrics(r, &loc, pose, world)
		ahead := math.Max(m.LeftFwdOff, m.RightFwdOff)
		if ahead > maxAhead {
			maxAhead = ahead
		}
	}
	if maxAhead < 0.05 {
		t.Fatalf("feet never passed in front of torso along travel, max ahead=%v", maxAhead)
	}
}

func TestStepStrideMatchesTravel(t *testing.T) {
	g := GaitParams{Speed: 0.2, StepRate: 1, Stride: 0.52}
	if s := g.StepStride(g.TravelSpeed(0.2)); math.Abs(s-0.2) > 1e-9 {
		t.Fatalf("step stride=%v want 0.2 for slow walk", s)
	}
}

func TestFormatGaitReport(t *testing.T) {
	s := FormatGaitReport(GaitReport{Frames: 10})
	if !strings.Contains(s, "10 frames") {
		t.Fatalf("unexpected report: %s", s)
	}
}
