package npc

import (
	"fmt"
	"os"
	"path/filepath"

	"raytracer/internal/character"
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// Manager owns runtime NPC agents and writes their poses into the scene.
type Manager struct {
	agents []Agent
	rigs   map[string]*character.Rig
}

// Agent is one instantiated character in the world.
type Agent struct {
	Name      string
	Rig       *character.Rig
	Pose      string
	Body      character.SpawnedBody
	Spawn     vec.V
	Yaw       float64
	Locomotor character.Locomotor
}

// NewManager returns an empty NPC manager.
func NewManager() *Manager {
	return &Manager{rigs: map[string]*character.Rig{}}
}

// Instantiate spawns all NPCs declared in sc.NPCSpawns, grounds them on the
// world surface, applies their initial pose, and calls sc.Touch() once.
func (m *Manager) Instantiate(sc *scene.Scene, world character.FootWorld) error {
	if sc == nil || len(sc.NPCSpawns) == 0 {
		return nil
	}
	m.agents = m.agents[:0]
	for i, sp := range sc.NPCSpawns {
		rig, err := m.loadRig(sp.Rig)
		if err != nil {
			return fmt.Errorf("npc[%d]: %w", i, err)
		}
		name := fmt.Sprintf("npc_%d", i)
		body, err := character.SpawnAttachments(rig, sc)
		if err != nil {
			return fmt.Errorf("npc[%d]: %w", i, err)
		}
		sc.DynamicBodies = append(sc.DynamicBodies, scene.DynamicBody{
			Name:      name,
			Boxes:     body.Boxes,
			Cylinders: body.Cylinders,
			Spheres:   body.Spheres,
		})
		heading := sp.Heading
		if heading == 0 && sp.Yaw != 0 {
			heading = sp.Yaw
		}
		agent := Agent{
			Name:  name,
			Rig:   rig,
			Pose:  sp.Pose,
			Body:  body,
			Spawn: sp.Pos,
			Yaw:   sp.Yaw,
			Locomotor: character.NewLocomotor(rig, sp.Pos, heading, sp.Speed, world),
		}
		m.applyAgent(sc, world, &agent)
		m.agents = append(m.agents, agent)
	}
	sc.Touch()
	return nil
}

// Update advances locomotion for all agents. Returns true if any pose changed.
func (m *Manager) Update(sc *scene.Scene, world character.FootWorld, dt float64) bool {
	if sc == nil || len(m.agents) == 0 {
		return false
	}
	changed := false
	for i := range m.agents {
		a := &m.agents[i]
		if a.Locomotor.Speed < 0.05 {
			continue
		}
		a.Locomotor.Update(dt, a.Rig, world)
		m.applyAgent(sc, world, a)
		changed = true
	}
	if changed {
		sc.Touch()
	}
	return changed
}

func (m *Manager) loadRig(path string) (*character.Rig, error) {
	abs, err := resolveRigPath(path)
	if err != nil {
		return nil, err
	}
	if r, ok := m.rigs[abs]; ok {
		return r, nil
	}
	r, err := character.LoadRig(abs)
	if err != nil {
		return nil, err
	}
	m.rigs[abs] = r
	return r, nil
}

func (m *Manager) applyAgent(sc *scene.Scene, world character.FootWorld, a *Agent) {
	pose := character.ComputeLocomotionPose(a.Rig, &a.Locomotor, a.Pose, world)
	character.ApplyPose(a.Rig, sc, a.Body, pose)
}

func resolveRigPath(rig string) (string, error) {
	if filepath.IsAbs(rig) {
		return rig, nil
	}
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, rig), nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}
