package character

// LocomotionParams tunes procedural biped locomotion per creature rig.
type LocomotionParams struct {
	RunSpeedThreshold  float64
	SwingFraction      float64
	FootOffsetLateral  float64
	StrideForwardLimit float64 // fraction of stride used to clamp forward foot offset

	FootLateralMin float64
	FootLateralMax float64

	StepUp          float64
	StepDown        float64
	StepUpMinHeight float64

	PelvisForwardProbe float64
	HeadClearance      float64

	Knee      KneeBendParams
	FootRoll  FootRollParams
	UpperBody UpperBodySwayParams

	// MultipedStepMode selects gait coordination for multiped step manager:
	// "queue" (default), "queue_wait", or "tetrapod".
	MultipedStepMode string
}

// FootPairSep returns the minimum lateral gap between left and right feet.
func (l LocomotionParams) FootPairSep() float64 {
	return 2 * l.FootLateralMin
}

// KneeBendParams controls minimum knee flex during IK.
type KneeBendParams struct {
	Stance              float64
	Swing               float64
	StepUpBase          float64
	StepUpScale         float64
	StanceDeep          float64
	HighAnkle           float64
	FallbackStance      float64
	FallbackStepUpBase  float64
	FallbackStepUpScale float64
	MinOutward          float64
}

// FootRollParams controls ankle roll through the gait cycle.
type FootRollParams struct {
	MaxDeg         float64
	SwingScale     float64
	ToeOffScale    float64
	HeelStrikeEnd  float64
	ToeOffStart    float64
}

// UpperBodySwayParams adds procedural motion to the torso and arms while walking.
type UpperBodySwayParams struct {
	ArmSwing    float64
	LateralSway float64
	SpinePitch  float64
	SpineRoll   float64
	HipRoll     float64
}

// NavigationParams sizes the pathfinding capsule for this creature.
type NavigationParams struct {
	Height          float64
	Radius          float64
	ClearanceRadius float64
	MaxStepUp       float64
}

// DefaultLocomotionParams returns humanoid-scale locomotion defaults.
func DefaultLocomotionParams() LocomotionParams {
	return LocomotionParams{
		RunSpeedThreshold:  3.5,
		SwingFraction:      0.38,
		FootOffsetLateral:  0.14,
		StrideForwardLimit: 0.55,
		FootLateralMin:     0.08,
		FootLateralMax:     0.17,
		StepUp:             0.50,
		StepDown:           1.20,
		StepUpMinHeight:    0.06,
		PelvisForwardProbe: 0.18,
		HeadClearance:      2.5,
		Knee: KneeBendParams{
			Stance:              18.0,
			Swing:               28.0,
			StepUpBase:          32.0,
			StepUpScale:         12.0,
			StanceDeep:          22.0,
			HighAnkle:           32.0,
			FallbackStance:      18.0,
			FallbackStepUpBase:  24.0,
			FallbackStepUpScale: 10.0,
			MinOutward:          0.06,
		},
		FootRoll: FootRollParams{
			MaxDeg:        26.0,
			SwingScale:    0.12,
			ToeOffScale:   0.55,
			HeelStrikeEnd: 0.15,
			ToeOffStart:   0.82,
		},
		UpperBody: UpperBodySwayParams{
			ArmSwing:    12.0,
			LateralSway: 0.025,
			SpinePitch:  1.5,
			SpineRoll:   2.0,
			HipRoll:     1.5,
		},
	}
}

// DefaultNavigationParams returns humanoid-scale navigation defaults.
func DefaultNavigationParams() NavigationParams {
	return NavigationParams{
		Height:          1.7,
		Radius:          0.3,
		ClearanceRadius: 0.45,
		MaxStepUp:       0.50,
	}
}

type locomotionYAML struct {
	RunSpeedThreshold  float64            `yaml:"run_speed_threshold"`
	SwingFraction      float64            `yaml:"swing_fraction"`
	FootOffsetLateral  float64            `yaml:"foot_offset_lateral"`
	StrideForwardLimit float64            `yaml:"stride_forward_limit"`
	FootLateralMin     float64            `yaml:"foot_lateral_min"`
	FootLateralMax     float64            `yaml:"foot_lateral_max"`
	StepUp             float64            `yaml:"step_up"`
	StepDown           float64            `yaml:"step_down"`
	StepUpMinHeight    float64            `yaml:"step_up_min_height"`
	PelvisForwardProbe float64            `yaml:"pelvis_forward_probe"`
	HeadClearance      float64            `yaml:"head_clearance"`
	Knee               kneeBendYAML       `yaml:"knee"`
	FootRoll           footRollYAML       `yaml:"foot_roll"`
	UpperBody          upperBodySwayYAML  `yaml:"upper_body"`
	MultipedStepMode string             `yaml:"multiped_step_mode"`
	SpiderStepMode   string             `yaml:"spider_step_mode"` // legacy YAML alias
}

type kneeBendYAML struct {
	Stance              float64 `yaml:"stance"`
	Swing               float64 `yaml:"swing"`
	StepUpBase          float64 `yaml:"step_up_base"`
	StepUpScale         float64 `yaml:"step_up_scale"`
	StanceDeep          float64 `yaml:"stance_deep"`
	HighAnkle           float64 `yaml:"high_ankle"`
	FallbackStance      float64 `yaml:"fallback_stance"`
	FallbackStepUpBase  float64 `yaml:"fallback_step_up_base"`
	FallbackStepUpScale float64 `yaml:"fallback_step_up_scale"`
	MinOutward          float64 `yaml:"min_outward"`
}

type footRollYAML struct {
	MaxDeg        float64 `yaml:"max_deg"`
	SwingScale    float64 `yaml:"swing_scale"`
	ToeOffScale   float64 `yaml:"toe_off_scale"`
	HeelStrikeEnd float64 `yaml:"heel_strike_end"`
	ToeOffStart   float64 `yaml:"toe_off_start"`
}

type upperBodySwayYAML struct {
	ArmSwing    float64 `yaml:"arm_swing"`
	LateralSway float64 `yaml:"lateral_sway"`
	SpinePitch  float64 `yaml:"spine_pitch"`
	SpineRoll   float64 `yaml:"spine_roll"`
	HipRoll     float64 `yaml:"hip_roll"`
}

type navigationYAML struct {
	Height          float64 `yaml:"height"`
	Radius          float64 `yaml:"radius"`
	ClearanceRadius float64 `yaml:"clearance_radius"`
	MaxStepUp       float64 `yaml:"max_step_up"`
}

func loadLocomotionParams(raw locomotionYAML) LocomotionParams {
	p := DefaultLocomotionParams()
	if raw.RunSpeedThreshold > 0 {
		p.RunSpeedThreshold = raw.RunSpeedThreshold
	}
	if raw.SwingFraction > 0 {
		p.SwingFraction = raw.SwingFraction
	}
	if raw.FootOffsetLateral > 0 {
		p.FootOffsetLateral = raw.FootOffsetLateral
	}
	if raw.StrideForwardLimit > 0 {
		p.StrideForwardLimit = raw.StrideForwardLimit
	}
	if raw.FootLateralMin > 0 {
		p.FootLateralMin = raw.FootLateralMin
	}
	if raw.FootLateralMax > 0 {
		p.FootLateralMax = raw.FootLateralMax
	}
	if raw.StepUp > 0 {
		p.StepUp = raw.StepUp
	}
	if raw.StepDown > 0 {
		p.StepDown = raw.StepDown
	}
	if raw.StepUpMinHeight > 0 {
		p.StepUpMinHeight = raw.StepUpMinHeight
	}
	if raw.PelvisForwardProbe > 0 {
		p.PelvisForwardProbe = raw.PelvisForwardProbe
	}
	if raw.HeadClearance > 0 {
		p.HeadClearance = raw.HeadClearance
	}
	if raw.MultipedStepMode != "" {
		p.MultipedStepMode = raw.MultipedStepMode
	} else if raw.SpiderStepMode != "" {
		p.MultipedStepMode = raw.SpiderStepMode
	}
	p.Knee = mergeKneeBend(p.Knee, raw.Knee)
	p.FootRoll = mergeFootRoll(p.FootRoll, raw.FootRoll)
	p.UpperBody = mergeUpperBody(p.UpperBody, raw.UpperBody)
	return p
}

func mergeKneeBend(base KneeBendParams, raw kneeBendYAML) KneeBendParams {
	if raw.Stance > 0 {
		base.Stance = raw.Stance
	}
	if raw.Swing > 0 {
		base.Swing = raw.Swing
	}
	if raw.StepUpBase > 0 {
		base.StepUpBase = raw.StepUpBase
	}
	if raw.StepUpScale > 0 {
		base.StepUpScale = raw.StepUpScale
	}
	if raw.StanceDeep > 0 {
		base.StanceDeep = raw.StanceDeep
	}
	if raw.HighAnkle > 0 {
		base.HighAnkle = raw.HighAnkle
	}
	if raw.FallbackStance > 0 {
		base.FallbackStance = raw.FallbackStance
	}
	if raw.FallbackStepUpBase > 0 {
		base.FallbackStepUpBase = raw.FallbackStepUpBase
	}
	if raw.FallbackStepUpScale > 0 {
		base.FallbackStepUpScale = raw.FallbackStepUpScale
	}
	if raw.MinOutward > 0 {
		base.MinOutward = raw.MinOutward
	}
	return base
}

func mergeFootRoll(base FootRollParams, raw footRollYAML) FootRollParams {
	if raw.MaxDeg > 0 {
		base.MaxDeg = raw.MaxDeg
	}
	if raw.SwingScale > 0 {
		base.SwingScale = raw.SwingScale
	}
	if raw.ToeOffScale > 0 {
		base.ToeOffScale = raw.ToeOffScale
	}
	if raw.HeelStrikeEnd > 0 {
		base.HeelStrikeEnd = raw.HeelStrikeEnd
	}
	if raw.ToeOffStart > 0 {
		base.ToeOffStart = raw.ToeOffStart
	}
	return base
}

func mergeUpperBody(base UpperBodySwayParams, raw upperBodySwayYAML) UpperBodySwayParams {
	if raw.ArmSwing > 0 {
		base.ArmSwing = raw.ArmSwing
	}
	if raw.LateralSway > 0 {
		base.LateralSway = raw.LateralSway
	}
	if raw.SpinePitch > 0 {
		base.SpinePitch = raw.SpinePitch
	}
	if raw.SpineRoll > 0 {
		base.SpineRoll = raw.SpineRoll
	}
	if raw.HipRoll > 0 {
		base.HipRoll = raw.HipRoll
	}
	return base
}

func loadNavigationParams(raw navigationYAML, loc LocomotionParams) NavigationParams {
	p := DefaultNavigationParams()
	if loc.StepUp > 0 {
		p.MaxStepUp = loc.StepUp
	}
	if raw.Height > 0 {
		p.Height = raw.Height
	}
	if raw.Radius > 0 {
		p.Radius = raw.Radius
	}
	if raw.ClearanceRadius > 0 {
		p.ClearanceRadius = raw.ClearanceRadius
	}
	if raw.MaxStepUp > 0 {
		p.MaxStepUp = raw.MaxStepUp
	}
	return p
}
