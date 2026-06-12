package audio

import (
	"math"
	"math/rand"
	"testing"
)

// allFinite reports whether every sample is a finite number within [-1, 1].
func allFinite(sig []float32) bool {
	for _, v := range sig {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) || f < -1.001 || f > 1.001 {
			return false
		}
	}
	return true
}

func TestSynthesizeProducesFiniteBuffers(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, mat := range []Surface{Grass, Hard, Wood} {
		buf := Synthesize(mat, 44100, rng)
		if len(buf) < 100 {
			t.Fatalf("surface %d: buffer too short (%d)", mat, len(buf))
		}
		if !allFinite(buf) {
			t.Fatalf("surface %d: buffer has NaN/Inf/out-of-range samples", mat)
		}
		var peak float32
		for _, v := range buf {
			if a := float32(math.Abs(float64(v))); a > peak {
				peak = a
			}
		}
		if peak < 0.1 {
			t.Fatalf("surface %d: buffer nearly silent (peak %v)", mat, peak)
		}
	}
}

func TestSynthesizeVariesPerCall(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a := Synthesize(Hard, 44100, rng)
	b := Synthesize(Hard, 44100, rng)
	same := len(a) == len(b)
	if same {
		identical := true
		for i := range a {
			if a[i] != b[i] {
				identical = false
				break
			}
		}
		if identical {
			t.Fatal("successive footsteps are bit-identical; expected per-step variation")
		}
	}
}

// TestReverbStable feeds an impulse and checks the tail decays and stays finite
// (no runaway feedback) over a couple of seconds.
func TestSynthesizeCrickets(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	buf := SynthesizeCrickets(44100, rng)
	if len(buf) < 44100 {
		t.Fatalf("crickets buffer too short (%d)", len(buf))
	}
	if !allFinite(buf) {
		t.Fatal("crickets buffer has NaN/Inf/out-of-range samples")
	}
	// Second call with a different seed should differ (not a static tone).
	buf2 := SynthesizeCrickets(44100, rand.New(rand.NewSource(99)))
	if len(buf2) != len(buf) {
		t.Fatalf("crickets length mismatch %d vs %d", len(buf), len(buf2))
	}
	same := true
	for i := range buf {
		if buf[i] != buf2[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("crickets synthesis should vary with RNG seed")
	}
}

func TestReverbStable(t *testing.T) {
	r := newReverb(44100)
	r.setParams(0.9, 0.3, 0.5, 0.5)
	maxTail := 0.0
	for i := 0; i < 44100*2; i++ {
		x := 0.0
		if i == 0 {
			x = 1
		}
		l, rr := r.process(x)
		if math.IsNaN(l) || math.IsInf(l, 0) || math.IsNaN(rr) || math.IsInf(rr, 0) {
			t.Fatalf("reverb produced non-finite output at sample %d", i)
		}
		if i > 44100 { // after 1s the tail should be decaying, not growing
			if a := math.Max(math.Abs(l), math.Abs(rr)); a > maxTail {
				maxTail = a
			}
		}
	}
	if maxTail > 0.5 {
		t.Fatalf("reverb tail not decaying (late peak %v)", maxTail)
	}
}

func TestBiquadLowpassFinite(t *testing.T) {
	lp := lowpass(400, 0.5, 44100)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		y := lp.process(rng.Float64()*2 - 1)
		if math.IsNaN(y) || math.IsInf(y, 0) {
			t.Fatalf("lowpass non-finite at %d", i)
		}
	}
}
