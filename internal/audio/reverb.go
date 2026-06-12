package audio

// reverb is a compact Freeverb-style stereo reverberator. Each channel has four
// parallel damped-feedback comb filters (which create the dense decaying echo
// tail) feeding two series all-pass filters (which smear the echoes so they
// sound diffuse rather than like discrete repeats). The right channel's delay
// lines are offset by stereoSpread samples so the two tails decorrelate into a
// wide stereo image. It is extremely cheap — a couple of dozen multiply-adds per
// sample — yet gives a convincing room whose size, brightness, and per-ear level
// are driven live from scene ray queries. Independent wetL/wetR let a wall on
// one side be heard mostly in that ear.
type reverb struct {
	combsL, combsR     []*comb
	allpassL, allpassR []*allpass
	feedback           float64 // comb feedback → decay time (room size)
	damp               float64 // high-frequency absorption in the tail
	wetL, wetR         float64 // per-channel reverb mix added on top of the dry signal
}

// Freeverb's tuned delay lengths (in samples at 44.1 kHz). They are mutually
// prime-ish so the comb echoes do not line up and ring. stereoSpread offsets the
// right channel so the two tails are decorrelated (classic Freeverb width).
var (
	combLens     = []int{1116, 1188, 1277, 1356}
	allpassLens  = []int{556, 441}
	stereoSpread = 23
)

func newReverb(sr int) *reverb {
	scale := float64(sr) / 44100.0
	r := &reverb{feedback: 0.84, damp: 0.3}
	for _, l := range combLens {
		r.combsL = append(r.combsL, &comb{buf: make([]float64, int(float64(l)*scale)+1)})
		r.combsR = append(r.combsR, &comb{buf: make([]float64, int(float64(l+stereoSpread)*scale)+1)})
	}
	for _, l := range allpassLens {
		r.allpassL = append(r.allpassL, &allpass{buf: make([]float64, int(float64(l)*scale)+1), fb: 0.5})
		r.allpassR = append(r.allpassR, &allpass{buf: make([]float64, int(float64(l+stereoSpread)*scale)+1), fb: 0.5})
	}
	return r
}

// setParams updates the reverb character. feedback in [0,1) sets decay length
// (bigger room / longer tail), damp in [0,1] rolls off the tail's highs, and
// wetL/wetR in [0,1] are how much reverb is mixed into each channel (0 = dry).
func (r *reverb) setParams(feedback, damp, wetL, wetR float64) {
	r.feedback = clamp01(feedback)
	r.damp = clamp01(damp)
	r.wetL = clamp01(wetL)
	r.wetR = clamp01(wetR)
}

// process returns the wet (reverb-only) output for one input sample as a stereo
// pair. The mixer adds wetL*l and wetR*r onto the dry signal.
func (r *reverb) process(x float64) (l, r2 float64) {
	for _, c := range r.combsL {
		l += c.process(x, r.feedback, r.damp)
	}
	l /= float64(len(r.combsL))
	for _, a := range r.allpassL {
		l = a.process(l)
	}
	for _, c := range r.combsR {
		r2 += c.process(x, r.feedback, r.damp)
	}
	r2 /= float64(len(r.combsR))
	for _, a := range r.allpassR {
		r2 = a.process(r2)
	}
	return l, r2
}

// comb is a single damped-feedback delay line.
type comb struct {
	buf   []float64
	idx   int
	store float64 // one-pole low-pass state for damping
}

func (c *comb) process(x, feedback, damp float64) float64 {
	out := c.buf[c.idx]
	c.store = out*(1-damp) + c.store*damp // damp the feedback path's highs
	c.buf[c.idx] = x + c.store*feedback
	c.idx++
	if c.idx >= len(c.buf) {
		c.idx = 0
	}
	return out
}

// allpass diffuses the comb output without coloring its magnitude.
type allpass struct {
	buf []float64
	idx int
	fb  float64
}

func (a *allpass) process(x float64) float64 {
	buffered := a.buf[a.idx]
	out := -x + buffered
	a.buf[a.idx] = x + buffered*a.fb
	a.idx++
	if a.idx >= len(a.buf) {
		a.idx = 0
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
