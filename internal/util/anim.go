package util

import "math"

// StepToward moves current toward target by at most maxStep.
func StepToward(current, target, maxStep float64) float64 {
	delta := target - current
	if math.Abs(delta) <= maxStep {
		return target
	}
	return current + math.Copysign(maxStep, delta)
}

// AtTarget reports whether current is within eps of target.
func AtTarget(current, target, eps float64) bool {
	return math.Abs(current-target) <= eps
}
