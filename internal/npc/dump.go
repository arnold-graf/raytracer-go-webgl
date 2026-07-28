package npc

import (
	"os"

	"raytracer/internal/character"
	"raytracer/internal/scene"
)

// DumpPoses simulates NPC locomotion for n frames and writes JSONL pose records.
func DumpPoses(sc *scene.Scene, world character.FootWorld, frames int, outPath string) error {
	return DumpPosesWithReport(sc, world, frames, outPath, "")
}

// DumpPosesWithReport writes JSONL and optionally a gait analysis report.
func DumpPosesWithReport(sc *scene.Scene, world character.FootWorld, frames int, outPath, reportPath string) error {
	m := NewManager()
	if err := m.Instantiate(sc, world); err != nil {
		return err
	}
	const dt = 1.0 / 60.0
	recs := make([]character.PoseRecord, 0, frames*len(m.agents))
	for frame := 0; frame < frames; frame++ {
		m.Update(sc, world, dt)
		m.collectPoseRecords(frame, world, &recs)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	if err := character.WritePoseRecords(f, recs); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if reportPath != "" {
		return WriteGaitReport(reportPath, recs)
	}
	return nil
}

// WriteGaitReport writes a human-readable gait analysis for pose records.
func WriteGaitReport(path string, recs []character.PoseRecord) error {
	rep := character.AnalyzePoseRecords(recs)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(character.FormatGaitReport(rep))
	if err != nil {
		return err
	}
	_, err = f.WriteString("\n\nPer-frame summary (last 20):\n")
	if err != nil {
		return err
	}
	start := 0
	if len(recs) > 20 {
		start = len(recs) - 20
	}
	for _, rec := range recs[start:] {
		_, err = f.WriteString(character.FormatFrameSummary(rec) + "\n")
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) collectPoseRecords(frame int, world character.FootWorld, recs *[]character.PoseRecord) {
	for i := range m.agents {
		a := &m.agents[i]
		if a.Driver == nil {
			continue
		}
		loc := a.Driver.Locomotor()
		if loc == nil {
			continue
		}
		pose := a.Driver.ComputePose(a.Rig, a.Pose, world)
		*recs = append(*recs, character.BuildPoseRecord(frame, a.Name, a.Rig, loc, pose, world))
	}
}

// DumpCurrentPoses appends one sample per agent to recs (live game loop).
func (m *Manager) DumpCurrentPoses(frame int, world character.FootWorld, recs *[]character.PoseRecord) {
	if m == nil {
		return
	}
	m.collectPoseRecords(frame, world, recs)
}

// WritePoseRecordsToFile writes JSONL pose records.
func WritePoseRecordsToFile(path string, recs []character.PoseRecord) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return character.WritePoseRecords(f, recs)
}

// DebugLines returns overlay segments for all agents.
func (m *Manager) DebugLines(world character.FootWorld) []character.DebugLine {
	if m == nil {
		return nil
	}
	var out []character.DebugLine
	for i := range m.agents {
		a := &m.agents[i]
		if a.Driver == nil {
			continue
		}
		loc := a.Driver.Locomotor()
		if loc == nil {
			continue
		}
		pose := a.Driver.ComputePose(a.Rig, a.Pose, world)
		out = append(out, character.DebugLines(a.Rig, loc, pose)...)
	}
	return out
}
