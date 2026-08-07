package audio

import (
	"math"
	"math/rand"
)

// SynthesizeSlideDoor builds a sci-fi sliding-door whoosh (~2.5 s at 1× playback).
// Play slower (pitch < 1) to stretch it across a long travel distance.
func SynthesizeSlideDoor(sr int, rng *rand.Rand) []float32 {
	fs := float64(sr)
	n := int(2.5 * fs)
	sig := make([]float64, n)

	// Initial pneumatic seal release.
	puff := noiseBurst(int(0.05*fs), 0.012, rng)
	runChain(puff, highpass(180, 0.6, fs), lowpass(1400, 0.5, fs))
	for i := range puff {
		if i < len(sig) {
			sig[i] += puff[i] * 0.42
		}
	}

	noise := make([]float64, n)
	for i := range noise {
		noise[i] = rng.Float64()*2 - 1
	}
	low := append([]float64(nil), noise...)
	mid := append([]float64(nil), noise...)
	high := append([]float64(nil), noise...)
	runChain(low, lowpass(700, 0.55, fs))
	runChain(mid, highpass(900, 0.5, fs), lowpass(2400, 0.5, fs))
	runChain(high, highpass(2600, 0.5, fs), lowpass(5200, 0.45, fs))

	for i := range sig {
		t := float64(i) / fs
		prog := float64(i) / float64(n-1)

		// Arch envelope with a little motor flutter.
		env := math.Sin(math.Pi * prog)
		env *= 0.9 + 0.1*math.Sin(2*math.Pi*13.5*t)

		motorHz := 88 + 28*prog
		motor := math.Sin(2*math.Pi*motorHz*t) * 0.22
		motor += 0.08 * math.Sin(2*math.Pi*motorHz*2.1*t)

		whoosh := low[i]*(1-prog)*0.55 + mid[i]*0.45 + high[i]*prog*0.5
		sig[i] += (motor + whoosh*0.72) * env
	}

	// Soft arrival thunk near the end.
	thunk := noiseBurst(int(0.035*fs), 0.008, rng)
	runChain(thunk, peaking(320, 6, 8, fs), lowpass(1800, 0.5, fs))
	thunkAt := int(0.9 * float64(n))
	for i := range thunk {
		if idx := thunkAt + i; idx < n {
			sig[idx] += thunk[i] * 0.28
		}
	}

	runChain(sig, lowpass(6000, 0.4, fs))
	softenAttack(sig, int(0.004*fs))
	applyFadeOut(sig, int(0.02*fs))
	normalize(sig, 0.52)
	return toF32(sig)
}

// reverseF32 returns a reversed copy of buf.
func reverseF32(buf []float32) []float32 {
	out := make([]float32, len(buf))
	for i, v := range buf {
		out[len(buf)-1-i] = v
	}
	return out
}
