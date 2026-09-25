package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
	"github.com/hajimehoshi/ebiten/v2/audio/vorbis"
	"github.com/hajimehoshi/ebiten/v2/audio/wav"
)

// fileLoopFade is the crossfade applied where a file-based loop wraps. Recorded
// ambience rarely ends where it began, so the seam needs a generous blend.
const fileLoopFade = 0.5

// LoadLoop decodes an audio file (mp3, ogg or wav) into a mono buffer at the
// engine's sample rate, ready to loop. The engine pans ambients itself, so the
// file's stereo image is folded down to mono.
func LoadLoop(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := bytes.NewReader(data)
	// Decode at the file's own rate: Ebiten's resampling decoders are several
	// times slower and returned a stretched stream for a 48 kHz mp3.
	var pcm interface {
		io.Reader
		SampleRate() int
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		pcm, err = mp3.DecodeWithoutResampling(src)
	case ".ogg":
		pcm, err = vorbis.DecodeWithoutResampling(src)
	case ".wav":
		pcm, err = wav.DecodeWithoutResampling(src)
	default:
		return nil, fmt.Errorf("unsupported audio format %q", path)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	raw, err := io.ReadAll(pcm)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	// Decoders emit 16-bit little-endian stereo frames.
	n := len(raw) / 4
	sig := make([]float64, n)
	for i := range sig {
		l := int16(binary.LittleEndian.Uint16(raw[i*4:]))
		r := int16(binary.LittleEndian.Uint16(raw[i*4+2:]))
		sig[i] = (float64(l) + float64(r)) / (2 * math.MaxInt16)
	}
	sig = resampleLinear(sig, pcm.SampleRate(), sampleRate)
	return toF32(overlapLoop(sig, int(fileLoopFade*sampleRate))), nil
}

// resampleLinear converts sig from rate from to rate to by linear
// interpolation, which is transparent for noisy ambience like fire crackle.
func resampleLinear(sig []float64, from, to int) []float64 {
	if from == to || from <= 0 || len(sig) < 2 {
		return sig
	}
	step := float64(from) / float64(to)
	out := make([]float64, int(float64(len(sig)-1)/step)+1)
	for i := range out {
		p := float64(i) * step
		j := int(p)
		if j >= len(sig)-1 {
			out[i] = sig[len(sig)-1]
			continue
		}
		f := p - float64(j)
		out[i] = sig[j]*(1-f) + sig[j+1]*f
	}
	return out
}

// overlapLoop makes a recording loop seamlessly by fading its last fade samples
// out over its first fade samples and dropping the tail. Playback then runs from
// the untouched middle straight into the blended head, which starts as the tail
// did, so the wrap is a continuation rather than a jump.
func overlapLoop(sig []float64, fade int) []float64 {
	n := len(sig)
	if fade <= 0 || fade*2 > n {
		return sig
	}
	tail := sig[n-fade:]
	for i := 0; i < fade; i++ {
		t := (float64(i) + 0.5) / float64(fade)
		// Equal-power weights: uncorrelated noise keeps a constant loudness.
		sig[i] = sig[i]*math.Sin(t*math.Pi/2) + tail[i]*math.Cos(t*math.Pi/2)
	}
	return sig[:n-fade]
}
