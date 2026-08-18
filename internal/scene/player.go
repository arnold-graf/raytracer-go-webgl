package scene

// PlayerMovement holds optional per-scene overrides from [player.movement].
// Zero float values and nil pointers mean "inherit from the global player config".
type PlayerMovement struct {
	WalkSpeed             float64
	EyeHeight             float64
	JumpVelocity          float64
	Gravity               float64
	CollisionRadius       float64
	StepHeight            float64
	SprintMultiplier      float64
	CrouchEyeHeight       float64
	CrouchSpeedMultiplier float64
	DoubleJumpEnabled     *bool
	JoltPhysics           *bool
}
