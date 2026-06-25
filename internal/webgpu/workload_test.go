package webgpu

import (
	"testing"
)

func TestAbsorbWorkloadSampleSmooths(t *testing.T) {
	r := &Renderer{}
	r.absorbWorkloadSample(GPUProfileCounters{Pixels: 100, PathSegs: 100})
	first := r.workload.PathSegsPerPx
	r.absorbWorkloadSample(GPUProfileCounters{Pixels: 100, PathSegs: 200})
	if r.workload.PathSegsPerPx <= first || r.workload.PathSegsPerPx >= 2 {
		t.Fatalf("smoothed paths/px = %v, want between %v and 2", r.workload.PathSegsPerPx, first)
	}
}
