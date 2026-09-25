package audio

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio"

	"raytracer/internal/vec"
)

// sampleRate is the engine's output rate. 44.1 kHz is plenty for footsteps and
// keeps the synthesized buffers small.
const sampleRate = 44100

// AmbientEmitter is a looping spatial sound placed in the world (loaded from
// [[sound]] in a scene file).
type AmbientEmitter struct {
	Sound  string
	File   string // audio file to loop instead of a synthesized Sound
	Pos    vec.V
	Gain   float64
	Radius float64
}

// Engine owns the Ebiten audio context, the streaming mixer, and the player
// that continuously pulls from it. One Engine exists per game.
type Engine struct {
	mixer    *Mixer
	player   *audio.Player
	rng      *rand.Rand
	ambients []AmbientEmitter
	// occ is the smoothed occlusion gain per ambient (1 = clear line of sight,
	// →0 = fully muffled behind walls), eased over time to avoid popping when the
	// listener crosses a doorway. bright is the matching high-frequency content,
	// which drops faster than loudness as the sound bends round obstacles.
	occ    []float64
	bright []float64
	// paths finds and remembers each ambient's route to the listener.
	paths []pathProbe
	// ambAtn is the smoothed distance attenuation per ambient (0 outside radius).
	ambAtn []float64
	// files caches decoded audio files by path, so rebuilding the ambients (a
	// campfire toggled on) doesn't decode the same recording again.
	files map[string][]float32
}

// NewEngine initializes the audio context and starts the streaming player. It
// returns an error if the platform cannot open an audio device; callers should
// treat audio as optional and continue silently on failure.
func NewEngine() (*Engine, error) {
	ctx := audio.NewContext(sampleRate)
	mixer := NewMixer(sampleRate)
	p, err := ctx.NewPlayerF32(mixer)
	if err != nil {
		return nil, err
	}
	// Keep the player's read-ahead buffer short. This buffer is the latency floor:
	// a freshly-added sound only plays once the already-queued audio drains, so
	// the buffer size is essentially the trigger-to-sound delay. Too small and the
	// audio thread can't refill between OS callbacks → underrun crackle. ~40 ms is
	// the sweet spot here: tight enough to feel immediate, large enough to be
	// glitch-free across GC/scheduling jitter.
	p.SetBufferSize(40 * time.Millisecond)
	p.Play()
	return &Engine{mixer: mixer, player: p, rng: rand.New(rand.NewSource(1))}, nil
}

// Footstep synthesizes and plays a footstep on the given surface. pitch scales
// the playback rate (1 = neutral); gain scales loudness. A fresh buffer is
// synthesized each call so successive steps differ naturally.
func (e *Engine) Footstep(mat Surface, gain, pitch float64) {
	if e == nil {
		return
	}
	buf := Synthesize(mat, sampleRate, e.rng)
	e.mixer.Add(buf, gain, pitch)
}

const slideDoorSynthDur = 2.5

// PlaySlideDoor plays a synthesized sci-fi sliding-door sound at pos. travelTime
// stretches playback to roughly match the door animation; opening=false plays
// the buffer reversed.
func (e *Engine) PlaySlideDoor(opening bool, travelTime float64, pos, listenerPos, listenerRight vec.V, gain float64) {
	if e == nil {
		return
	}
	buf := SynthesizeSlideDoor(sampleRate, e.rng)
	if !opening {
		buf = reverseF32(buf)
	}
	pitch := slideDoorSynthDur / travelTime
	if pitch < 0.2 {
		pitch = 0.2
	}
	if pitch > 2 {
		pitch = 2
	}
	gainL, gainR := panGains(pos, listenerPos, listenerRight, gain)
	e.mixer.AddPan(buf, gainL, gainR, pitch)
}

func panGains(src, listenerPos, listenerRight vec.V, gain float64) (l, r float64) {
	if gain <= 0 {
		return 0, 0
	}
	dx := src.X - listenerPos.X
	dz := src.Z - listenerPos.Z
	horiz := vec.V{X: dx, Z: dz}
	if horiz.LenSq() < 1e-12 {
		return gain * 0.707, gain * 0.707
	}
	horiz = horiz.Normalize()
	pan := horiz.Dot(listenerRight)
	rW := clampPan((pan + 1) * 0.5)
	lW := 1 - rW
	return gain * math.Sqrt(lW), gain * math.Sqrt(rW)
}

// SetReverb updates the room reverb (see Mixer.SetReverb). wetL/wetR are the
// per-ear reverb levels for directional reflections.
func (e *Engine) SetReverb(feedback, damp, wetL, wetR float64) {
	if e == nil {
		return
	}
	e.mixer.SetReverb(feedback, damp, wetL, wetR)
}

// SetAmbients replaces all looping spatial sources from the scene. Each emitter
// gets a unique synthesized loop (slightly different pitch/phase) so multiple
// tree crickets don't sound identical.
func (e *Engine) SetAmbients(emitters []AmbientEmitter) {
	if e == nil {
		return
	}
	voices := make([]*ambientVoice, 0, len(emitters))
	kept := make([]AmbientEmitter, 0, len(emitters))
	for i, em := range emitters {
		var buf []float32
		sub := rand.New(rand.NewSource(int64(i*7919 + 42)))
		switch {
		case em.File != "":
			buf = e.loadFile(em.File)
		case em.Sound == "crickets":
			buf = SynthesizeCrickets(sampleRate, sub)
		case em.Sound == "fan":
			buf = SynthesizeFan(sampleRate, sub)
		default:
			continue
		}
		if len(buf) == 0 {
			continue
		}
		kept = append(kept, em)

		n := float64(len(buf))
		var heads []*ambientHead
		switch {
		case em.File != "":
			// A recording already varies on its own; extra heads would only
			// double every crackle. A random start keeps two fires apart.
			heads = []*ambientHead{
				{pos: sub.Float64() * n, speed: 1.0, gain: 1.0},
			}
		case em.Sound == "crickets":
			// Three read heads at slightly different, mutually-incommensurate speeds
			// and random phases. Their sum drifts continuously so the chirp pattern
			// never repeats on the buffer's period, killing the obvious loop.
			heads = []*ambientHead{
				{pos: sub.Float64() * n, speed: 1.000, gain: 0.62},
				{pos: sub.Float64() * n, speed: 0.937 + sub.Float64()*0.02, gain: 0.5},
				{pos: sub.Float64() * n, speed: 1.063 + sub.Float64()*0.02, gain: 0.42},
			}
		case em.Sound == "fan":
			// Single head: a steady loop avoids extra wrap points that can click on drones.
			heads = []*ambientHead{
				{pos: sub.Float64() * n, speed: 1.0, gain: 1.0},
			}
		}
		voices = append(voices, &ambientVoice{buf: buf, heads: heads, lpAlpha: 1})
	}
	e.ambients = kept
	e.occ = make([]float64, len(kept))
	e.bright = make([]float64, len(kept))
	e.ambAtn = make([]float64, len(kept))
	e.paths = make([]pathProbe, len(kept))
	e.mixer.SetAmbients(voices)
}

// loadFile returns the decoded loop for an audio file, decoding it on first
// use. A file that fails to decode is reported once and stays silent.
func (e *Engine) loadFile(path string) []float32 {
	if buf, ok := e.files[path]; ok {
		return buf
	}
	buf, err := LoadLoop(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audio: %v\n", err)
	}
	if e.files == nil {
		e.files = make(map[string][]float32)
	}
	e.files[path] = buf
	return buf
}

// UpdateAmbients recomputes distance attenuation, stereo pan, and wall
// occlusion for every ambient emitter. cast answers the ray queries that find
// how sound gets round obstacles; nil treats every path as clear. Call once per
// frame.
func (e *Engine) UpdateAmbients(listenerPos, listenerRight vec.V, cast RayCast) {
	if e == nil || len(e.ambients) == 0 {
		return
	}
	gL := make([]float64, len(e.ambients))
	gR := make([]float64, len(e.ambients))
	lp := make([]float64, len(e.ambients))
	for i := range lp {
		lp[i] = 1
	}
	const occEase = 0.25 // ~0.4 s to settle at 60 Hz polls
	for i, em := range e.ambients {
		dx := em.Pos.X - listenerPos.X
		dy := em.Pos.Y - listenerPos.Y
		dz := em.Pos.Z - listenerPos.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		targetAtn := 0.0
		if em.Radius > 0 && dist < em.Radius {
			t := 1 - dist/em.Radius
			targetAtn = t * t * em.Gain
		}
		e.ambAtn[i] += (targetAtn - e.ambAtn[i]) * occEase
		if e.ambAtn[i] < 1e-6 {
			e.ambAtn[i] = 0
			e.occ[i] += (0 - e.occ[i]) * occEase
			e.bright[i] += (0 - e.bright[i]) * occEase
			e.paths[i] = pathProbe{}
			continue
		}
		atten := e.ambAtn[i]

		// Ray-traced occlusion: sound that has to bend round an obstacle comes
		// quieter and duller the longer its detour. Eased so stepping through a
		// doorway fades rather than pops.
		targetOcc, targetBright := 1.0, 1.0
		if cast != nil {
			targetOcc, targetBright = e.paths[i].update(listenerPos, em.Pos, cast, e.rng)
		}
		e.occ[i] += (targetOcc - e.occ[i]) * occEase
		e.bright[i] += (targetBright - e.bright[i]) * occEase
		atten *= e.occ[i]
		lp[i] = lowpassAlpha(e.bright[i])

		// Equal-power pan from horizontal direction relative to the listener.
		// Sound taking a detour arrives from the detour, not through the wall.
		if p := &e.paths[i]; p.hasBest {
			dx, dz = p.best.X-listenerPos.X, p.best.Z-listenerPos.Z
		}
		horiz := vec.V{X: dx, Z: dz}
		if horiz.LenSq() < 1e-12 {
			gL[i], gR[i] = atten*0.707, atten*0.707
			continue
		}
		horiz = horiz.Normalize()
		pan := horiz.Dot(listenerRight) // -1 left, +1 right
		rW := clampPan((pan + 1) * 0.5)
		lW := 1 - rW
		gL[i] = atten * math.Sqrt(lW)
		gR[i] = atten * math.Sqrt(rW)
	}
	e.mixer.UpdateAmbientGains(gL, gR, lp)
}

// Occluded ambients are low-passed with a cutoff swept from mutedCutoff (fully
// muffled) up to openCutoff (clear, where the filter is bypassed).
const (
	mutedCutoff = 500.0
	openCutoff  = 16000.0
)

// lowpassAlpha maps brightness (0 = muffled, 1 = clear) to a one-pole low-pass
// coefficient, sweeping the cutoff logarithmically so each step sounds even.
func lowpassAlpha(bright float64) float64 {
	if bright >= 0.99 {
		return 1
	}
	b := math.Max(bright, 0)
	fc := mutedCutoff * math.Pow(openCutoff/mutedCutoff, b)
	return 1 - math.Exp(-2*math.Pi*fc/sampleRate)
}

func clampPan(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
