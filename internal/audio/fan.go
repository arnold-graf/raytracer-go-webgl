package audio

import (
	"math"
	"math/rand"
)

// SynthesizeFan builds a seamlessly looping old-computer-fan buffer (~2.5 s).
// Each call randomizes motor pitch, blade rate, bearing whine, and slow
// amplitude flutter so neighboring racks don't sound identical.
func SynthesizeFan(sr int, rng *rand.Rand) []float32 {
	fs := float64(sr)
	dur := 2.5
	n := int(dur * fs)
	sig := make([]float64, n)

	// Integer cycle counts over the buffer so periodic layers wrap without a seam.
	humHz := float64(220+rng.Intn(36)) / dur    // ~88–102 Hz
	bladeHz := float64(35+rng.Intn(26)) / dur   // ~14–24 Hz
	flutterHz := float64(1+rng.Intn(2)) / dur   // 0.4 or 0.8 Hz
	whineHz := float64(4000+rng.Intn(1200)) / dur
	modHz := float64(8) / dur // integer cycles so FM layer loops cleanly
	flutterPh := rng.Float64() * 2 * math.Pi

	noise := make([]float64, n)
	for i := range noise {
		noise[i] = rng.Float64()*2 - 1
	}
	runChain(noise, highpass(180, 0.5, fs), lowpass(2800, 0.5, fs))
	loopCrossfade(noise, int(0.18*fs))

	for i := range sig {
		t := float64(i) / fs
		flutter := 0.88 + 0.12*math.Sin(2*math.Pi*flutterHz*t+flutterPh)

		hum := math.Sin(2 * math.Pi * humHz * t)
		hum += 0.35 * math.Sin(2*math.Pi*humHz*2*t)
		hum += 0.15 * math.Sin(2*math.Pi*humHz*3*t+0.3)

		bladePulse := 0.35 + 0.65*math.Max(0, math.Sin(2*math.Pi*bladeHz*t))
		blade := noise[i] * bladePulse

		whine := 0.06 * math.Sin(2*math.Pi*whineHz*t+0.7*math.Sin(2*math.Pi*modHz*t))

		sig[i] = (hum*0.45 + blade*0.42 + whine) * flutter
	}

	runChain(sig, lowpass(4200, 0.4, fs))
	loopCrossfade(sig, int(0.2*fs))
	normalize(sig, 0.55)
	repairLoopSeam(sig, int(0.06*fs))
	return toF32(sig)
}
