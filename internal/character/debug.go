package character

import (
	"encoding/json"
	"io"

	"raytracer/internal/vec"
)

// DebugLine is a world-space overlay segment for skeleton/foot debugging.
type DebugLine struct {
	From vec.V
	To   vec.V
	Kind string // bone, foot, target
}

// FootRecord is one foot's runtime state for pose dumps.
type FootRecord struct {
	World   [3]float64 `json:"world"`
	Plant   [3]float64 `json:"plant"`
	Phase   string     `json:"phase"`
	StanceT float64    `json:"stance_t"`
	SwingT  float64    `json:"swing_t"`
	RollDeg float64    `json:"roll_deg"`
}

// PoseRecord is a JSON-friendly snapshot of one agent at one instant.
type PoseRecord struct {
	Frame   int                     `json:"frame"`
	Agent   string                  `json:"agent"`
	Phase   float64                 `json:"locomotor_phase"`
	Heading float64                 `json:"heading"`
	Speed   float64                 `json:"speed"`
	Hip     [3]float64              `json:"hip"`
	Left    FootRecord              `json:"left_foot"`
	Right   FootRecord              `json:"right_foot"`
	Bones   map[string][3]float64   `json:"bones"`
	Metrics GaitMetrics             `json:"metrics"`
}

// BuildPoseRecord captures locomotor + bone joint positions for dumping.
func BuildPoseRecord(frame int, agent string, rig *Rig, loc *Locomotor, pose SkeletonPose, world FootWorld) PoseRecord {
	rec := PoseRecord{
		Frame:   frame,
		Agent:   agent,
		Phase:   loc.Phase,
		Heading: loc.Heading,
		Speed:   loc.Speed,
		Hip:     vec3Array(loc.HipPos),
		Left:    footRecord(loc.Left, rig.Locomotion.FootRoll),
		Right:   footRecord(loc.Right, rig.Locomotion.FootRoll),
		Bones:   make(map[string][3]float64, len(rig.BoneOrder)),
		Metrics: ComputeGaitMetrics(rig, loc, pose, world),
	}
	for _, name := range rig.BoneOrder {
		if xf := pose.Bones[name]; xf != nil {
			rec.Bones[name] = vec3Array(xf.Translation())
		}
	}
	return rec
}

func footRecord(f Foot, roll FootRollParams) FootRecord {
	return FootRecord{
		World:   vec3Array(f.World),
		Plant:   vec3Array(f.PlantWorld),
		Phase:   f.Phase.String(),
		StanceT: f.StanceT,
		SwingT:  f.SwingT,
		RollDeg: footRollDeg(f.Phase, f.StanceT, roll),
	}
}

func vec3Array(v vec.V) [3]float64 {
	return [3]float64{v.X, v.Y, v.Z}
}

// WritePoseRecords writes one JSON object per line (JSONL).
func WritePoseRecords(w io.Writer, recs []PoseRecord) error {
	enc := json.NewEncoder(w)
	for _, rec := range recs {
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}

// DebugLines returns skeleton and foot overlay segments for the current pose.
func DebugLines(rig *Rig, loc *Locomotor, pose SkeletonPose) []DebugLine {
	var out []DebugLine
	add := func(from, to vec.V, kind string) {
		out = append(out, DebugLine{From: from, To: to, Kind: kind})
	}

	hips := pose.Bones["hips"]
	if hips == nil {
		return out
	}
	hipPos := hips.Translation()
	fwd := yawForward(loc.Heading)
	add(hipPos, hipPos.Add(fwd.Scale(0.35)), "travel")
	add(hipPos, hipPos.Add(vec.V{Y: 0.12}), "target")

	for _, chain := range []struct{ thigh, shin, foot string }{
		{"thigh_l", "shin_l", "foot_l"},
		{"thigh_r", "shin_r", "foot_r"},
	} {
		socket := hips.ToWorld(rig.JointLocal(chain.thigh))
		shinXF := pose.Bones[chain.shin]
		if shinXF == nil {
			continue
		}
		kneePos := shinXF.Translation()
		add(socket, kneePos, "bone")
		if pose.Bones[chain.foot] != nil {
			toe := rig.BoneTip(pose, chain.foot)
			add(kneePos, toe, "bone")
		}
	}

	for _, f := range []Foot{loc.Left, loc.Right} {
		add(f.World, f.World.Add(vec.V{Y: 0.06}), "foot")
		add(f.PlantWorld, f.PlantWorld.Add(vec.V{Y: 0.04}), "target")
		// Ground cross under contact for sole vs floor inspection.
		add(f.World.Add(vec.V{X: -0.05, Z: 0}), f.World.Add(vec.V{X: 0.05, Z: 0}), "ground")
		add(f.World.Add(vec.V{Z: -0.05}), f.World.Add(vec.V{Z: 0.05}), "ground")
	}
	return out
}
