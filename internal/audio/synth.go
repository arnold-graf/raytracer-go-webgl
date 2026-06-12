package audio

import (
	"math"
	"math/rand"
)

// Surface is the acoustic material of a footstep, mirroring scene.StepMaterial.
// The audio package keeps its own enum so it does not depend on the scene.
type Surface int

const (
	Grass Surface = iota
	Hard
	Wood
	Snow
)

// noiseBurst returns n samples of white noise shaped by an exponential decay
// envelope: a short percussive "click" that the resonant filters then color.
// decay is a fraction of the buffer length (smaller = snappier).
func noiseBurst(n int, decay float64, rng *rand.Rand) []float64 {
	out := make([]float64, n)
	tau := float64(n) * decay
	for i := range out {
		env := math.Exp(-float64(i) / tau)
		out[i] = (rng.Float64()*2 - 1) * env
	}
	return out
}

// softenAttack ramps the first ramp samples up from zero so a sound starts as a
// gentle swell rather than a percussive click — useful for soft surfaces.
func softenAttack(sig []float64, ramp int) {
	if ramp <= 0 || ramp > len(sig) {
		return
	}
	for i := 0; i < ramp; i++ {
		sig[i] *= float64(i) / float64(ramp)
	}
}

// normalize scales sig so its peak magnitude equals peak (no-op for silence).
func normalize(sig []float64, peak float64) {
	m := 0.0
	for _, v := range sig {
		if a := math.Abs(v); a > m {
			m = a
		}
	}
	if m == 0 {
		return
	}
	g := peak / m
	for i := range sig {
		sig[i] *= g
	}
}

// toF32 converts a float64 working buffer to the float32 PCM the mixer plays.
func toF32(sig []float64) []float32 {
	out := make([]float32, len(sig))
	for i, v := range sig {
		out[i] = float32(v)
	}
	return out
}

// Synthesize builds a fresh footstep buffer for the given surface. A new buffer
// is generated every call so the random exciter differs slightly each step,
// giving natural variation on top of the per-step pitch jitter applied at
// playback.
func Synthesize(mat Surface, sr int, rng *rand.Rand) []float32 {
	switch mat {
	case Hard:
		return synthHard(sr, rng)
	case Wood:
		return synthWood(sr, rng)
	case Snow:
		return synthSnow(sr, rng)
	default:
		return synthGrass(sr, rng)
	}
}
