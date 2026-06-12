package audio

import (
	"math"
	"sync"
)

// ambientHead is one read position into a shared loop buffer, advancing at its
// own speed. Several heads at incommensurate speeds/phases sum to a texture that
// never exactly repeats even though they share one short buffer.
type ambientHead struct {
	pos   float64
	speed float64
	gain  float64
}

func (h *ambientHead) sample(buf []float32) float64 {
	n := len(buf)
	i := int(h.pos)
	if i >= n {
		i = n - 1
	}
	frac := h.pos - float64(i)
	var s float64
	if i < n-1 {
		s = float64(buf[i])*(1-frac) + float64(buf[i+1])*frac
	} else {
		s = float64(buf[i])*(1-frac) + float64(buf[0])*frac // wrap interpolation
	}
	h.pos += h.speed
	if h.pos >= float64(n) {
		h.pos -= float64(n)
	}
	return s * h.gain
}

// ambientVoice is a looping spatial sound source built from several read heads
// over a shared buffer so it never repeats identically. gainL/gainR (the
// distance/pan/occlusion mix) are updated each frame from the listener.
type ambientVoice struct {
	buf   []float32
	heads []*ambientHead
	gainL float64
	gainR float64
}

func (a *ambientVoice) sample() (l, r float64) {
	if len(a.buf) == 0 {
		return 0, 0
	}
	var s float64
	for _, h := range a.heads {
		s += h.sample(a.buf)
	}
	return s * a.gainL, s * a.gainR
}

// voice is one playing one-shot sound: a synthesized buffer read back at a
// variable speed (for pitch variation) and scaled by gain.
type voice struct {
	buf   []float32
	pos   float64
	speed float64 // playback rate; >1 raises pitch and shortens duration
	gain  float64
}

// done reports whether the voice has played past the end of its buffer.
func (v *voice) done() bool { return v.pos >= float64(len(v.buf)-1) }

// sample reads the buffer at the current fractional position with linear
// interpolation and advances by speed.
func (v *voice) sample() float64 {
	i := int(v.pos)
	if i >= len(v.buf)-1 {
		return 0
	}
	frac := v.pos - float64(i)
	s := float64(v.buf[i])*(1-frac) + float64(v.buf[i+1])*frac
	v.pos += v.speed
	return s * v.gain
}

// Mixer is the streaming audio source handed to Ebiten. It sums all active
// voices, runs the master through the reverb, and emits 32-bit float stereo PCM.
// It is read from Ebiten's audio goroutine and mutated from the game goroutine,
// so every field touched by both is guarded by mu.
type Mixer struct {
	mu       sync.Mutex
	sr       int
	voices   []*voice
	ambients []*ambientVoice
	rev      *reverb
}

// NewMixer creates a mixer at the given sample rate with reverb initially dry.
func NewMixer(sr int) *Mixer {
	return &Mixer{sr: sr, rev: newReverb(sr)}
}

// Add queues a sound for playback. speed scales pitch/length (1 = as recorded);
// gain scales amplitude. Safe to call from the game loop.
func (m *Mixer) Add(buf []float32, gain, speed float64) {
	if len(buf) == 0 {
		return
	}
	if speed <= 0 {
		speed = 1
	}
	m.mu.Lock()
	m.voices = append(m.voices, &voice{buf: buf, speed: speed, gain: gain})
	m.mu.Unlock()
}

// SetAmbients replaces all looping spatial sources (e.g. tree crickets).
func (m *Mixer) SetAmbients(amb []*ambientVoice) {
	m.mu.Lock()
	m.ambients = amb
	m.mu.Unlock()
}

// UpdateAmbientGains sets per-ear levels for each ambient voice (caller holds
// the slice exclusively between SetAmbients and the next SetAmbients).
func (m *Mixer) UpdateAmbientGains(gainsL, gainsR []float64) {
	m.mu.Lock()
	for i := range m.ambients {
		if i < len(gainsL) {
			m.ambients[i].gainL = gainsL[i]
		}
		if i < len(gainsR) {
			m.ambients[i].gainR = gainsR[i]
		}
	}
	m.mu.Unlock()
}

// SetReverb updates the room reverb parameters (see reverb.setParams). wetL/wetR
// are the per-ear reverb levels, letting a wall on one side be heard mostly in
// that ear. Safe to call from the game loop; takes effect on the next block.
func (m *Mixer) SetReverb(feedback, damp, wetL, wetR float64) {
	m.mu.Lock()
	m.rev.setParams(feedback, damp, wetL, wetR)
	m.mu.Unlock()
}

// Read implements io.Reader, producing little-endian float32 stereo frames (8
// bytes per frame). It never blocks or returns EOF: when nothing is playing it
// emits silence (still passed through the reverb so tails ring out), keeping the
// Ebiten player alive for the life of the game.
func (m *Mixer) Read(p []byte) (int, error) {
	frames := len(p) / 8
	m.mu.Lock()
	for f := 0; f < frames; f++ {
		var dry, ambL, ambR float64
		for _, v := range m.voices {
			dry += v.sample()
		}
		for _, a := range m.ambients {
			l, r := a.sample()
			ambL += l
			ambR += r
		}
		// The dry footstep stays centered (it's at the player's feet); the reverb
		// tail is panned per ear so reflections favor the side with nearby walls.
		// Ambients are already panned and bypass reverb (outdoor insect chorus).
		wl, wr := m.rev.process(dry)
		sL := clip(dry + m.rev.wetL*wl + ambL)
		sR := clip(dry + m.rev.wetR*wr + ambR)
		lb := math.Float32bits(float32(sL))
		rb := math.Float32bits(float32(sR))
		o := f * 8
		p[o] = byte(lb)
		p[o+1] = byte(lb >> 8)
		p[o+2] = byte(lb >> 16)
		p[o+3] = byte(lb >> 24)
		p[o+4] = byte(rb)
		p[o+5] = byte(rb >> 8)
		p[o+6] = byte(rb >> 16)
		p[o+7] = byte(rb >> 24)
	}
	m.reap()
	m.mu.Unlock()
	return frames * 8, nil
}

// clip hard-limits a sample to [-1, 1].
func clip(s float64) float64 {
	if s > 1 {
		return 1
	}
	if s < -1 {
		return -1
	}
	return s
}

// reap removes finished voices in place (caller holds mu).
func (m *Mixer) reap() {
	keep := m.voices[:0]
	for _, v := range m.voices {
		if !v.done() {
			keep = append(keep, v)
		}
	}
	m.voices = keep
}
