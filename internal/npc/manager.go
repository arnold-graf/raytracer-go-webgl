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
	Name   string
	Rig    *character.Rig
	Pose   string
	Body   character.SpawnedBody
	Spawn  vec.V
	Yaw    float64
	Driver character.LocomotionDriver
	Nav    *Navigator
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
		nav := NewNavigator(sp)
		if nav != nil {
			if len(sp.Patrol) > 0 {
				for len(sp.Patrol) > 1 && nav.atTarget(sp.Pos, heading, rig.Navigation) {
					next := (nav.wpIdx + 1) % len(sp.Patrol)
					if next == nav.wpIdx {
						break
					}
					nav.wpIdx = next
				}
			}
			heading = nav.InitialHeading(sp.Pos)
		}
		speed := sp.Speed
		if nav != nil && speed < 0.05 {
			speed = nav.walkSpeed
		}
		agent := Agent{
			Name:   name,
			Rig:    rig,
			Pose:   sp.Pose,
			Body:   body,
			Spawn:  sp.Pos,
			Yaw:    sp.Yaw,
			Nav:    nav,
			Driver: character.NewLocomotionDriver(rig, sp.Pos, heading, speed, world, sp.Pose),
		}
		m.applyAgentPose(sc, world, &agent)
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
	peers := make([]*Agent, len(m.agents))
	for i := range m.agents {
		peers[i] = &m.agents[i]
	}
	for i := range m.agents {
		a := &m.agents[i]
		if a.Nav != nil {
			a.Nav.Tick(a, sc, peers, dt)
		}
		if a.Driver == nil {
			continue
		}
		if a.Driver.AlwaysAnimate() || a.Driver.Speed() >= 0.05 {
			a.Driver.Update(dt, a.Rig, world)
			changed = true
		}
	}
	if len(m.agents) > 1 {
		m.resolveSeparation(world)
		changed = true
	}
	for i := range m.agents {
		a := &m.agents[i]
		if a.Driver == nil {
			continue
		}
		if a.Driver.AlwaysAnimate() || a.Driver.Speed() >= 0.05 {
			m.applyAgent(sc, world, a)
		}
	}
	if changed {
		sc.TouchTransforms()
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

func (m *Manager) applyAgentPose(sc *scene.Scene, world character.FootWorld, a *Agent) {
	m.applyAgent(sc, world, a)
}

func (m *Manager) applyAgent(sc *scene.Scene, world character.FootWorld, a *Agent) {
	if a.Driver == nil {
		return
	}
	pose := a.Driver.ComputePose(a.Rig, a.Pose, world)
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
