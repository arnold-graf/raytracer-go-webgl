package audio

import (
	"math"
	"math/rand"
)

// synthGrass generates a footstep on grass: a soft compression of the turf
// followed by an airy "swish" of blades springing back. Unlike the snow step
// (which is dark and muffled), grass keeps a brighter, livelier blade layer —
// but it is still soft, with no thump and no sharp brush-stroke transient. The
// blade rustle is gently amplitude-modulated so it reads as many blades brushing
// rather than flat noise.
func synthGrass(sr int, rng *rand.Rand) []float32 {
	fs := float64(sr)
	n := int(0.16 * fs) // ~160 ms

	// Body: the soft give of soil/turf. Medium decay — softer than a hard floor,
	// a touch snappier than snow. Thump removed, but kept brighter than snow so
	// the two surfaces are clearly distinct.
	body := noiseBurst(n, 0.32, rng)
	bhp := highpass(180, 0.6, fs)
	blp := lowpass(2200, 0.6, fs)
	runChain(body, bhp, blp)

	// Blade swish: an airy, longer band-limited layer that gives grass its
	// characteristic rustle. Higher and brighter than the snow rustle.
	swish := noiseBurst(int(0.14*fs), 0.55, rng)
	shp := highpass(1700, 0.5, fs)
	slp := lowpass(6000, 0.7, fs)
	runChain(swish, shp, slp)

	// Modulate the swish with a slow, slightly randomized wobble so it sounds
	// like blades brushing past each other instead of a uniform hiss.
	wob := 22 + rng.Float64()*14 // 22–36 Hz
	phase := rng.Float64() * 2 * math.Pi
	for i := range swish {
		t := float64(i) / fs
		swish[i] *= 0.6 + 0.4*math.Sin(2*math.Pi*wob*t+phase)
	}

	for i := range swish {
		body[i] += swish[i] * 0.38
	}

	softenAttack(body, int(0.012*fs)) // gentle onset, no click
	normalize(body, 0.24)              // soft, between snow and the harder surfaces
	applyFadeOut(body, int(0.04*fs))
	return toF32(body)
}
