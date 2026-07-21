package camera

import "raytracer/internal/scene"

// MergeConfig applies scene [player.movement] overrides onto base. Omitted/zero
// fields keep base values.
func MergeConfig(base Config, o scene.PlayerMovement) Config {
	c := base
	if o.WalkSpeed != 0 {
		c.WalkSpeed = o.WalkSpeed
	}
	if o.EyeHeight != 0 {
		c.EyeHeight = o.EyeHeight
	}
	if o.JumpVelocity != 0 {
		c.JumpVelocity = o.JumpVelocity
	}
	if o.Gravity != 0 {
		c.Gravity = o.Gravity
	}
	if o.CollisionRadius != 0 {
		c.CollisionRadius = o.CollisionRadius
	}
	if o.StepHeight != 0 {
		c.StepHeight = o.StepHeight
	}
	if o.SprintMultiplier != 0 {
		c.SprintMultiplier = o.SprintMultiplier
	}
	if o.CrouchEyeHeight != 0 {
		c.CrouchEyeHeight = o.CrouchEyeHeight
	}
	if o.CrouchSpeedMultiplier != 0 {
		c.CrouchSpeedMultiplier = o.CrouchSpeedMultiplier
	}
	if o.DoubleJumpEnabled != nil {
		c.DoubleJumpEnabled = *o.DoubleJumpEnabled
	}
	return c
}
