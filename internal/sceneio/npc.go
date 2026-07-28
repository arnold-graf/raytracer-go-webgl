package sceneio

import (
	"fmt"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

type npcDTO struct {
	Rig     string   `toml:"rig"`
	Pose    string   `toml:"pose"`
	At      vec3     `toml:"at"`
	Yaw     float64  `toml:"yaw"`
	Speed   float64  `toml:"speed"`
	Heading *float64 `toml:"heading"`
	Patrol       []vec3   `toml:"patrol"`
	Goal         *vec3    `toml:"goal"`
	TargetRadius float64  `toml:"target_radius"`
}

func (d npcDTO) build() (scene.NPCSpawn, error) {
	pose := d.Pose
	if pose == "" {
		pose = "idle"
	}
	heading := d.Yaw
	if d.Heading != nil {
		heading = *d.Heading
	}
	var patrol []vec.V
	for _, p := range d.Patrol {
		patrol = append(patrol, p.toV())
	}
	var goal *vec.V
	if d.Goal != nil {
		g := d.Goal.toV()
		goal = &g
	}
	if len(patrol) > 0 && goal != nil {
		return scene.NPCSpawn{}, fmt.Errorf("npc: set patrol or goal, not both")
	}
	return scene.NPCSpawn{
		Rig:          d.Rig,
		Pose:         pose,
		Pos:          d.At.toV(),
		Yaw:          d.Yaw,
		Speed:        d.Speed,
		Heading:      heading,
		Patrol:       patrol,
		Goal:         goal,
		TargetRadius: d.TargetRadius,
	}, nil
}
