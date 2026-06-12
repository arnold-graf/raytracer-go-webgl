package audio

import "math/rand"

// synthSnow generates a footstep on snow: a soft, dark, compressing "shff" with
// almost no bright content — the sound of powder packing underfoot. The low,
// slow-decaying body and the very faint, dark rustle give the muffled, squeaky
// compression of snow rather than any sharp transient. (This is the gentle
// effect that originally stood in for grass; it suits snow far better.)
func synthSnow(sr int, rng *rand.Rand) []float32 {
	n := int(0.18 * float64(sr)) // ~180 ms, long and gentle
	// Slow decay so the step is a soft compression rather than a percussive
	// transient.
	sig := noiseBurst(n, 0.45, rng)

	// Cut the thump (below ~200 Hz) and roll off the harsh top hard; what remains
	// is a soft, dark mid "compression" of the powder underfoot.
	hp := highpass(200, 0.6, float64(sr))
	lp := lowpass(1300, 0.6, float64(sr))
	runChain(sig, hp, lp)

	// A faint, soft, dark rustle of packing snow — present for life, never bright.
	rustle := noiseBurst(int(0.12*float64(sr)), 0.4, rng)
	rhp := highpass(1100, 0.6, float64(sr))
	rlp := lowpass(3800, 0.7, float64(sr))
	runChain(rustle, rhp, rlp)
	for i := range rustle {
		sig[i] += rustle[i] * 0.22
	}

	softenAttack(sig, int(0.02*float64(sr))) // slow swell, no click
	normalize(sig, 0.2)                       // soft and quiet
	applyFadeOut(sig, int(0.05*float64(sr)))
	return toF32(sig)
}
