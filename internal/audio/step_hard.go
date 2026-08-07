package audio

import "math/rand"

// synthHard generates a footstep on a hard surface (stone, marble, tile): a
// crisp "tock" with body in the low mids and a short ring, but without the
// separate high heel-click layer that made earlier versions too sharp.
func synthHard(sr int, rng *rand.Rand) []float32 {
	n := int(0.09 * float64(sr)) // ~90 ms
	sig := noiseBurst(n, 0.06, rng)

	body := peaking(180, 5, 8, float64(sr))
	ring := peaking(900, 4, 6, float64(sr))
	tick := peaking(1450, 3, 4, float64(sr))
	hp := highpass(380, 0.5, float64(sr))
	lp := lowpass(3800, 0.55, float64(sr))
	runChain(sig, body, ring, tick, hp, lp)

	softenAttack(sig, int(0.008*float64(sr)))
	normalize(sig, 0.4)
	applyFadeOut(sig, int(0.012*float64(sr)))
	return toF32(sig)
}

// applyFadeOut tapers the last fade samples linearly to zero so a clipped tail
// never clicks.
func applyFadeOut(sig []float64, fade int) {
	if fade <= 0 || fade > len(sig) {
		fade = len(sig)
	}
	start := len(sig) - fade
	for i := 0; i < fade; i++ {
		g := 1 - float64(i)/float64(fade)
		sig[start+i] *= g
	}
}
