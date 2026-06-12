// Package audio synthesizes and plays the game's sound effects. Everything is
// generated procedurally (no sample files): a short noise "exciter" is shaped by
// material-specific resonant filters to produce footsteps, and a fast Freeverb
// reverb whose parameters are driven by scene ray queries places those sounds in
// the room. The whole thing is mixed by a streaming source fed to Ebiten.
package audio

import "math"

// biquad is a transposed-direct-form-II second-order IIR filter. Coefficients
// follow the RBJ audio EQ cookbook; state is carried in z1/z2 between samples.
type biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
	z1, z2     float64
}

// process runs one input sample through the filter and returns the output.
func (f *biquad) process(x float64) float64 {
	y := f.b0*x + f.z1
	f.z1 = f.b1*x - f.a1*y + f.z2
	f.z2 = f.b2*x - f.a2*y
	return y
}

// lowpass builds a 2nd-order low-pass at cutoff freq (Hz) with quality q.
func lowpass(freq, q, sr float64) *biquad {
	w0 := 2 * math.Pi * freq / sr
	cw, sw := math.Cos(w0), math.Sin(w0)
	alpha := sw / (2 * q)
	a0 := 1 + alpha
	return normBiquad((1-cw)/2, 1-cw, (1-cw)/2, a0, -2*cw, 1-alpha)
}

// highpass builds a 2nd-order high-pass at cutoff freq (Hz) with quality q.
func highpass(freq, q, sr float64) *biquad {
	w0 := 2 * math.Pi * freq / sr
	cw, sw := math.Cos(w0), math.Sin(w0)
	alpha := sw / (2 * q)
	a0 := 1 + alpha
	return normBiquad((1+cw)/2, -(1 + cw), (1+cw)/2, a0, -2*cw, 1-alpha)
}

// peaking builds a peaking EQ bell of gainDB at center freq (Hz) with quality q.
// Large q and gain give a narrow, ringing resonance (a modal partial).
func peaking(freq, q, gainDB, sr float64) *biquad {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * freq / sr
	cw, sw := math.Cos(w0), math.Sin(w0)
	alpha := sw / (2 * q)
	a0 := 1 + alpha/A
	return normBiquad(1+alpha*A, -2*cw, 1-alpha*A, a0, -2*cw, 1-alpha/A)
}

// normBiquad divides all coefficients by a0 and returns the filter.
func normBiquad(b0, b1, b2, a0, a1, a2 float64) *biquad {
	return &biquad{b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: a1 / a0, a2: a2 / a0}
}

// runChain passes the signal through every filter in order, in place.
func runChain(sig []float64, filters ...*biquad) {
	for _, f := range filters {
		for i, x := range sig {
			sig[i] = f.process(x)
		}
	}
}
