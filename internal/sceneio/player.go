package sceneio

import (
	"fmt"

	"github.com/BurntSushi/toml"

	"raytracer/internal/camera"
)

// movementDTO mirrors the [movement] table of a player-config TOML file.
type movementDTO struct {
	WalkSpeed             float64 `toml:"walk_speed"`
	EyeHeight             float64 `toml:"eye_height"`
	JumpVelocity          float64 `toml:"jump_velocity"`
	Gravity               float64 `toml:"gravity"`
	CollisionRadius       float64 `toml:"collision_radius"`
	StepHeight            float64 `toml:"step_height"`
	SprintMultiplier      float64 `toml:"sprint_multiplier"`
	CrouchEyeHeight       float64 `toml:"crouch_eye_height"`
	CrouchSpeedMultiplier float64 `toml:"crouch_speed_multiplier"`
}

type playerDTO struct {
	Movement movementDTO `toml:"movement"`
}

// LoadPlayer reads a player-movement config from disk. Omitted fields keep their
// built-in defaults.
func LoadPlayer(path string) (camera.Config, error) {
	var dto playerDTO
	if _, err := toml.DecodeFile(path, &dto); err != nil {
		return camera.Config{}, fmt.Errorf("load player config %q: %w", path, err)
	}
	return dto.config(), nil
}

// DecodePlayer decodes a player-movement config from memory (for the embedded
// default).
func DecodePlayer(data []byte) (camera.Config, error) {
	var dto playerDTO
	if err := toml.Unmarshal(data, &dto); err != nil {
		return camera.Config{}, fmt.Errorf("decode player config: %w", err)
	}
	return dto.config(), nil
}

func (d playerDTO) config() camera.Config {
	c := camera.DefaultConfig()
	m := d.Movement
	if m.WalkSpeed != 0 {
		c.WalkSpeed = m.WalkSpeed
	}
	if m.EyeHeight != 0 {
		c.EyeHeight = m.EyeHeight
	}
	if m.JumpVelocity != 0 {
		c.JumpVelocity = m.JumpVelocity
	}
	if m.Gravity != 0 {
		c.Gravity = m.Gravity
	}
	if m.CollisionRadius != 0 {
		c.CollisionRadius = m.CollisionRadius
	}
	if m.StepHeight != 0 {
		c.StepHeight = m.StepHeight
	}
	if m.SprintMultiplier != 0 {
		c.SprintMultiplier = m.SprintMultiplier
	}
	if m.CrouchEyeHeight != 0 {
		c.CrouchEyeHeight = m.CrouchEyeHeight
	}
	if m.CrouchSpeedMultiplier != 0 {
		c.CrouchSpeedMultiplier = m.CrouchSpeedMultiplier
	}
	return c
}
