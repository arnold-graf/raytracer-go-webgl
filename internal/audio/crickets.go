package audio

import (
	"math"
	"math/rand"
)

// SynthesizeCrickets builds a seamlessly looping cricket chorus buffer (~4 s).
// Several overlapping chirp bursts at slightly different carrier frequencies and
// pulse rates give a natural night-insect texture rather than a single metronome.
func SynthesizeCrickets(sr int, rng *rand.Rand) []float32 {
	fs := float64(sr)
	n := int(4.0 * fs)
	sig := make([]float64, n)

	// Scatter chirp bursts through the loop. Each burst is a short train of
	// high-frequency pulses — the classic field-cricket "chirp".
	numBursts := 10 + rng.Intn(6)
	for b := 0; b < numBursts; b++ {
		start := rng.Intn(n)
		carrier := 4000 + rng.Float64()*2000 // 4–6 kHz
		pulseHz := 28 + rng.Float64()*14     // ~28–42 pulses/s within the chirp
		dur := int((0.05 + rng.Float64()*0.09) * fs)
		amp := 0.22 + rng.Float64()*0.18

		for i := 0; i < dur; i++ {
			t := float64(i) / fs
			// Pulse train: rectified sine shapes each "click" in the chirp.
			pulse := math.Sin(2 * math.Pi * pulseHz * t)
			if pulse < 0 {
				pulse = 0
			}
			// Overall chirp envelope: gentle rise and fall.
			chirpEnv := math.Sin(math.Pi * float64(i) / float64(dur))
			carrierSig := math.Sin(2*math.Pi*carrier*t) * (0.7 + 0.3*math.Sin(2*math.Pi*carrier*0.03*t))
			idx := (start + i) % n
			sig[idx] += carrierSig * pulse * chirpEnv * amp
		}
	}

	// Faint continuous insect hiss underneath the chirps.
	hiss := noiseBurst(n, 0.85, rng)
	runChain(hiss, highpass(3800, 0.5, fs), lowpass(9000, 0.5, fs))
	for i := range sig {
		sig[i] += hiss[i] * 0.035
	}

	// Band-limit and crossfade the loop ends so playback wraps without a click.
	runChain(sig, highpass(3200, 0.6, fs), lowpass(9500, 0.6, fs))
	loopCrossfade(sig, int(0.1*fs))
	normalize(sig, 0.5)
	return toF32(sig)
}

// loopCrossfade blends the tail of sig into its head so circular playback is
// seamless. Only the head is rewritten; the tail is ramped to match the head at
// the wrap point. Earlier versions also attenuated the start of the tail region
// to zero, which caused a periodic pop mid-buffer on steady drones like fans.
func loopCrossfade(sig []float64, fade int) {
	if fade <= 0 || fade*2 > len(sig) {
		return
	}
	n := len(sig)
	for i := 0; i < fade; i++ {
		t := float64(i) / float64(fade)
		w := 0.5 - 0.5*math.Cos(math.Pi*t)
		sig[i] = sig[i]*(1-w) + sig[n-fade+i]*w
	}
	blendTailToHead(sig, fade)
}

// blendTailToHead pulls the last fade samples toward sig[0] so circular playback
// is sample-continuous at the wrap point.
func blendTailToHead(sig []float64, fade int) {
	if fade <= 0 || fade >= len(sig) {
		return
	}
	n := len(sig)
	target := sig[0]
	for i := 0; i < fade; i++ {
		w := 1 - float64(i)/float64(fade) // n-1 gets w=1, earlier tail samples blend less
		idx := n - 1 - i
		sig[idx] = sig[idx]*(1-w) + target*w
	}
}

// repairLoopSeam re-applies blendTailToHead after processing that may have
// reopened a wrap discontinuity (e.g. peak normalization).
func repairLoopSeam(sig []float64, fade int) {
	blendTailToHead(sig, fade)
}
