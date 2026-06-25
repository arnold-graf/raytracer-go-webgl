package app

import (
	"testing"
	"time"
)

func TestHudSmootherConverges(t *testing.T) {
	var s hudSmoother
	base := time.Now()
	s.last = base

	for i := 0; i < 120; i++ {
		s.last = base.Add(time.Duration(i) * time.Millisecond * 16)
		gpu, fps := s.sample(10, 100)
		if i == 119 {
			if gpu < 9 || gpu > 10.5 {
				t.Fatalf("gpu after 2s smooth = %v, want ~10", gpu)
			}
			if fps < 95 || fps > 100.5 {
				t.Fatalf("fps after 2s smooth = %v, want ~100", fps)
			}
		}
	}
}

func TestHudSmootherIgnoresEarlyZero(t *testing.T) {
	var s hudSmoother
	gpu, fps := s.sample(0, 0)
	if s.ready || gpu != 0 || fps != 0 {
		t.Fatalf("expected not ready on zeros, got ready=%v gpu=%v fps=%v", s.ready, gpu, fps)
	}
	gpu, fps = s.sample(8, 120)
	if !s.ready || gpu != 8 || fps != 120 {
		t.Fatalf("first real sample = gpu %v fps %v ready %v", gpu, fps, s.ready)
	}
}
