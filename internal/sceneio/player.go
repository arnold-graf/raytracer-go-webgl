package sceneio

import (
	"fmt"

	"github.com/BurntSushi/toml"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
)

// movementDTO mirrors [movement] / [player.movement] TOML tables.
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
	DoubleJumpEnabled     *bool   `toml:"double_jump_enabled"`
}

func (m movementDTO) toScene() scene.PlayerMovement {
	return scene.PlayerMovement(m)
}

type playerDTO struct {
	Movement movementDTO `toml:"movement"`
}

type playerSceneDTO struct {
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
	return camera.MergeConfig(camera.DefaultConfig(), d.Movement.toScene())
}
