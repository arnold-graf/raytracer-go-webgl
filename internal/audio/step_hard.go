package audio

import "math/rand"

// synthHard generates a footstep on a hard surface (stone, marble, tile): a
// bright, short "tick" with a little high-mid ring and almost no body. It is a
// fast noise burst shaped by a high-pass (kill the low rumble) and a peaking
// resonance around 1.2 kHz, matching the marble filter chain in the reference.
func synthHard(sr int, rng *rand.Rand) []float32 {
	n := int(0.07 * float64(sr)) // ~70 ms
	sig := noiseBurst(n, 0.04, rng)

	hp := highpass(600, 0.8, float64(sr))
	peak := peaking(1200, 2, 6, float64(sr))
	runChain(sig, hp, peak)

	// A second, very short click adds the initial heel "snap".
	click := noiseBurst(int(0.012*float64(sr)), 0.02, rng)
	hp2 := highpass(2500, 0.7, float64(sr))
	runChain(click, hp2)
	for i := range click {
		sig[i] += click[i] * 0.6
	}

	normalize(sig, 0.5)
	applyFadeOut(sig, int(0.01*float64(sr)))
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
