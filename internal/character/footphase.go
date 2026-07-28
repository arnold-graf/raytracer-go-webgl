package character

// FootPhase is the explicit stance/swing segment for one foot.
type FootPhase int

const (
	FootSwing FootPhase = iota
	FootHeelStrike
	FootMidStance
	FootToeOff
)

func (p FootPhase) String() string {
	switch p {
	case FootSwing:
		return "swing"
	case FootHeelStrike:
		return "heel_strike"
	case FootMidStance:
		return "stance"
	case FootToeOff:
		return "toe_off"
	default:
		return "unknown"
	}
}

func stanceSubPhase(stanceT float64, roll FootRollParams) FootPhase {
	switch {
	case stanceT < roll.HeelStrikeEnd:
		return FootHeelStrike
	case stanceT > roll.ToeOffStart:
		return FootToeOff
	default:
		return FootMidStance
	}
}

// footRollDeg returns ankle pitch (degrees) for toe-up (+) / toe-down (−) roll.
func footRollDeg(phase FootPhase, stanceT float64, roll FootRollParams) float64 {
	maxRoll := roll.MaxDeg
	switch phase {
	case FootSwing:
		// Toe slightly raised during swing.
		return maxRoll * roll.SwingScale
	case FootHeelStrike:
		u := clamp01(stanceT / roll.HeelStrikeEnd)
		return maxRoll * (1 - u)
	case FootMidStance:
		return 0
	case FootToeOff:
		u := clamp01((stanceT - roll.ToeOffStart) / (1 - roll.ToeOffStart))
		return -maxRoll * roll.ToeOffScale * u
	default:
		return 0
	}
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
