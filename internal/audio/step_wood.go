package audio

import (
	"math"
	"math/rand"
)

// synthWood generates a footstep on a wooden floor: a deep, hollow "tock" with
// the board's low modal partials, a low suspended-floor cavity boom, and a
// friction creak on most steps. Earlier versions sat too high (the cavity mode
// was over 1 kHz and the highs weren't rolled off), so the partials here are
// dropped well down and a low-pass removes the clicky top.
func synthWood(sr int, rng *rand.Rand) []float32 {
	n := int(0.17 * float64(sr)) // ~170 ms, plenty of body
	sig := noiseBurst(n, 0.08, rng)

	// Deep board modes (a board "tock", not a "tick").
	m1 := peaking(120, 7, 12, float64(sr))
	m2 := peaking(185, 8, 11, float64(sr))
	m3 := peaking(280, 9, 9, float64(sr))
	// Hollow cavity boom of a suspended floor — a low resonance is what makes it
	// sound hollow rather than solid.
	cavity := peaking(90, 5, 11, float64(sr))
	// Roll off the highs so the step reads as low/hollow, not high-pitched.
	lp := lowpass(2000, 0.7, float64(sr))
	runChain(sig, m1, m2, m3, cavity, lp)

	// Creak on roughly half the steps: a sustained, amplitude-wobbled mid
	// resonance (stick-slip friction of the boards), delayed slightly after the
	// impact.
	if rng.Float64() < 0.5 {
		addCreak(sig, sr, rng)
	}

	softenAttack(sig, int(0.012*float64(sr))) // gentler onset, less of a hard knock
	normalize(sig, 0.38)                       // softer footfall, not a marching stomp
	applyFadeOut(sig, int(0.03*float64(sr)))
	return toF32(sig)
}

// addCreak layers a friction-creak onto a wood step in place: noise driven
// through a narrow mid resonance and its octave, amplitude-modulated by a slow
// "stick-slip" wobble so it sounds like a squeaking board rather than a tone.
func addCreak(sig []float64, sr int, rng *rand.Rand) {
	n := len(sig)
	creak := noiseBurst(n, 0.45, rng) // slow decay → sustained squeak
	f := 180 + rng.Float64()*180      // ~180–360 Hz: a low, woody squeak
	cf1 := peaking(f, 16, 16, float64(sr))
	// A gentle octave for body, but kept low-gain/low-Q and rolled off so it
	// reads as wood, not a metallic hi-hat ring.
	cf2 := peaking(f*2.0, 7, 6, float64(sr))
	lp := lowpass(1400, 0.7, float64(sr))
	runChain(creak, cf1, cf2, lp)

	wob := 16 + rng.Float64()*18 // 16–34 Hz stick-slip rate
	for i := range creak {
		t := float64(i) / float64(sr)
		creak[i] *= (0.5 + 0.5*math.Sin(2*math.Pi*wob*t)) * 0.55
	}

	d := int(0.015 * float64(sr)) // start the creak just after the heel impact
	for i := 0; i+d < n; i++ {
		sig[i+d] += creak[i]
	}
}
