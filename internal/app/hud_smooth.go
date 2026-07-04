package app

import (
	"math"
	"time"
)

const hudSmoothTau = time.Second

// hudSmoother low-passes HUD timing numbers with a ~1 s time constant so they
// are readable while the underlying frame times jitter.
type hudSmoother struct {
	last  time.Time
	gpuMS float64
	fps   float64
	ready bool
}

func (s *hudSmoother) sample(gpuMS, fps float64) (smoothGPU, smoothFPS float64) {
	now := time.Now()
	alpha := 1.0
	if !s.last.IsZero() {
		dt := now.Sub(s.last).Seconds()
		if dt > 0 {
			alpha = 1 - math.Exp(-dt/hudSmoothTau.Seconds())
		}
	}
	s.last = now

	if !s.ready {
		s.gpuMS = gpuMS
		s.fps = fps
		s.ready = gpuMS > 0 || fps > 0
	} else {
		s.gpuMS += alpha * (gpuMS - s.gpuMS)
		s.fps += alpha * (fps - s.fps)
	}
	return s.gpuMS, s.fps
}

func (s *hudSmoother) reset() {
	s.last = time.Time{}
	s.gpuMS = 0
	s.fps = 0
	s.ready = false
}
