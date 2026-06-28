package character

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"raytracer/internal/vec"
)

// GaitMetrics captures per-frame locomotion diagnostics for pose dumps.
type GaitMetrics struct {
	TravelFwd   [3]float64 `json:"travel_fwd"`
	GroundY     float64    `json:"ground_y"`
	LeftFwdOff  float64    `json:"left_fwd_off"`  // foot vs hip along travel (+ = foot ahead)
	RightFwdOff float64    `json:"right_fwd_off"`
	LeftSoleY   float64    `json:"left_sole_y"`
	RightSoleY  float64    `json:"right_sole_y"`
	LeftKneeDeg float64    `json:"left_knee_deg"`
	RightKneeDeg float64   `json:"right_knee_deg"`
}

// GaitReport summarizes a pose dump for gait sanity checks.
type GaitReport struct {
	Frames              int
	StanceBackwardSkate int // stance frames where planted foot moved opposite travel
	SwingBackwardArc    int // swing frames where foot moved opposite travel
	MaxStanceBackVel    float64
	MaxSwingBackVel     float64
	MinSwingFwdVel      float64
}

// ComputeGaitMetrics derives inspection metrics from a locomotion pose.
func ComputeGaitMetrics(rig *Rig, loc *Locomotor, pose SkeletonPose, world FootWorld) GaitMetrics {
	fwd := yawForward(loc.Heading)
	m := GaitMetrics{
		TravelFwd: vec3Array(fwd),
		GroundY:   loc.GroundY,
	}
	if world != nil {
		headY := groundHeadHint(loc, loc.GroundY+rig.HipHeight)
		if gy := world.GroundHeight(loc.HipPos.X, loc.HipPos.Z, headY); gy > m.GroundY {
			m.GroundY = gy
		}
	}
	hip := loc.HipPos
	m.LeftFwdOff = loc.Left.World.Sub(hip).Dot(fwd)
	m.RightFwdOff = loc.Right.World.Sub(hip).Dot(fwd)
	m.LeftSoleY = soleY(rig, pose, "foot_l")
	m.RightSoleY = soleY(rig, pose, "foot_r")
	m.LeftKneeDeg = kneeFlexDeg(rig, pose, "thigh_l", "shin_l")
	m.RightKneeDeg = kneeFlexDeg(rig, pose, "thigh_r", "shin_r")
	return m
}

func soleY(rig *Rig, pose SkeletonPose, footName string) float64 {
	return footSoleWorld(rig, pose, footName).Y
}

func kneeFlexDeg(rig *Rig, pose SkeletonPose, thigh, shin string) float64 {
	hips := pose.Bones["hips"]
	shinXF := pose.Bones[shin]
	if hips == nil || shinXF == nil {
		return 0
	}
	hip := hips.ToWorld(rig.JointLocal(thigh))
	knee := shinXF.Translation()
	ankle := rig.BoneTip(pose, shin)
	thighDir := knee.Sub(hip).Normalize()
	shinDir := ankle.Sub(knee).Normalize()
	dot := thighDir.Dot(shinDir)
	if dot > 1 {
		dot = 1
	}
	if dot < -1 {
		dot = -1
	}
	return 180 - math.Acos(dot)*180/math.Pi
}

// AnalyzePoseRecords scans a dump for backward foot skating / moonwalk cues.
func AnalyzePoseRecords(recs []PoseRecord) GaitReport {
	var rep GaitReport
	if len(recs) == 0 {
		return rep
	}
	rep.Frames = len(recs)
	prev := recs[0]
	for i := 1; i < len(recs); i++ {
		cur := recs[i]
		analyzeStep(&rep, prev, cur)
		prev = cur
	}
	return rep
}

func analyzeStep(rep *GaitReport, prev, cur PoseRecord) {
	fwd := vecFromArray(cur.Metrics.TravelFwd)
	for _, side := range []struct {
		phase string
		prevW [3]float64
		curW  [3]float64
	}{
		{cur.Left.Phase, prev.Left.World, cur.Left.World},
		{cur.Right.Phase, prev.Right.World, cur.Right.World},
	} {
		if side.phase == "" {
			continue
		}
		vel := vecFromArray(side.curW).Sub(vecFromArray(side.prevW))
		fwdVel := vel.Dot(fwd)
		switch side.phase {
		case "swing":
			if fwdVel < -0.0005 {
				rep.SwingBackwardArc++
				if -fwdVel > rep.MaxSwingBackVel {
					rep.MaxSwingBackVel = -fwdVel
				}
			}
		default:
			if fwdVel < -0.0005 {
				rep.StanceBackwardSkate++
				if -fwdVel > rep.MaxStanceBackVel {
					rep.MaxStanceBackVel = -fwdVel
				}
			}
		}
	}
}

func vecFromArray(a [3]float64) vec.V {
	return vec.V{X: a[0], Y: a[1], Z: a[2]}
}

// FormatGaitReport renders a human-readable gait analysis.
func FormatGaitReport(r GaitReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Gait report (%d frames)\n", r.Frames)
	fmt.Fprintf(&b, "  stance backward skate frames: %d (max vel %.4f m/f)\n", r.StanceBackwardSkate, r.MaxStanceBackVel)
	fmt.Fprintf(&b, "  swing backward arc frames:    %d (max vel %.4f m/f)\n", r.SwingBackwardArc, r.MaxSwingBackVel)
	if r.StanceBackwardSkate > 0 || r.SwingBackwardArc > r.Frames/10 {
		b.WriteString("  ⚠ possible moonwalk: feet moving opposite travel direction\n")
	} else {
		b.WriteString("  ✓ foot motion generally aligned with travel\n")
	}
	return b.String()
}

// ReadPoseRecords loads JSONL pose records.
func ReadPoseRecords(path string) ([]PoseRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadPoseRecordsFrom(f)
}

// ReadPoseRecordsFrom loads JSONL pose records from r.
func ReadPoseRecordsFrom(r io.Reader) ([]PoseRecord, error) {
	var recs []PoseRecord
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var rec PoseRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	return recs, sc.Err()
}

// FormatFrameSummary renders one line per frame for quick terminal inspection.
func FormatFrameSummary(rec PoseRecord) string {
	return fmt.Sprintf("f%4d phase=%.2f L:%s fwd=%+.3f soleY=%.3f | R:%s fwd=%+.3f soleY=%.3f",
		rec.Frame, rec.Phase, rec.Left.Phase, rec.Metrics.LeftFwdOff, rec.Metrics.LeftSoleY,
		rec.Right.Phase, rec.Metrics.RightFwdOff, rec.Metrics.RightSoleY)
}
