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

// swingFraction is the walk-cycle portion spent in swing (rest is stance).
const swingFraction = 0.38

func stanceSubPhase(stanceT float64) FootPhase {
	switch {
	case stanceT < 0.15:
		return FootHeelStrike
	case stanceT > 0.82:
		return FootToeOff
	default:
		return FootMidStance
	}
}

// footRollDeg returns ankle pitch (degrees) for toe-up (+) / toe-down (−) roll.
func footRollDeg(phase FootPhase, stanceT float64) float64 {
	const maxRoll = 26.0
	switch phase {
	case FootSwing:
		// Toe slightly raised during swing.
		return maxRoll * 0.12
	case FootHeelStrike:
		u := clamp01(stanceT / 0.15)
		return maxRoll * (1 - u)
	case FootMidStance:
		return 0
	case FootToeOff:
		u := clamp01((stanceT - 0.82) / 0.18)
		return -maxRoll * 0.55 * u
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
