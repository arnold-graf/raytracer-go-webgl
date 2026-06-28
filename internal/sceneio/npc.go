package sceneio

import "raytracer/internal/scene"

type npcDTO struct {
	Rig     string  `toml:"rig"`
	Pose    string  `toml:"pose"`
	At      vec3    `toml:"at"`
	Yaw     float64 `toml:"yaw"`
	Speed   float64 `toml:"speed"`
	Heading *float64 `toml:"heading"`
}

func (d npcDTO) build() scene.NPCSpawn {
	pose := d.Pose
	if pose == "" {
		pose = "idle"
	}
	heading := d.Yaw
	if d.Heading != nil {
		heading = *d.Heading
	}
	return scene.NPCSpawn{
		Rig:     d.Rig,
		Pose:    pose,
		Pos:     d.At.toV(),
		Yaw:     d.Yaw,
		Speed:   d.Speed,
		Heading: heading,
	}
}
