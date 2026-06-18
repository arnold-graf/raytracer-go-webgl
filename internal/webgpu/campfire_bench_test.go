package webgpu

import (
	"testing"
	"time"
	"unsafe"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func BenchmarkCampfireParams_3(b *testing.B) {
	sc := &scene.Scene{
		Campfires: []scene.Campfire{
			{Center: vec.New(0, 0.1, 0), Color: vec.New(1, 0.6, 0.3),
				Range: 3, Brightness: 5, Jitter: 0.02, Flicker: 0.4, Speed: 1, Seed: 0},
			{Center: vec.New(5, 0.1, 3), Color: vec.New(1, 0.7, 0.4),
				Range: 4, Brightness: 6, Jitter: 0.02, Flicker: 0.4, Speed: 1, Seed: 1},
			{Center: vec.New(-3, 0.1, 2), Color: vec.New(1, 0.65, 0.35),
				Range: 3.5, Brightness: 5.5, Jitter: 0.025, Flicker: 0.35, Speed: 1.1, Seed: 2},
		},
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = PackCampfireParams(sc)
	}
}

func BenchmarkCampfireParams_10(b *testing.B) {
	sc := &scene.Scene{}
	for i := range 10 {
		sc.Campfires = append(sc.Campfires, scene.Campfire{
			Center: vec.New(float64(i), 0.1, 0), Color: vec.New(1, 0.6, 0.3),
			Range: 3, Brightness: 5, Jitter: 0.02, Flicker: 0.4, Speed: 1, Seed: float64(i)})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = PackCampfireParams(sc)
	}
}

// cpuCampfireResolution simulates what the old per-frame PackCampfires
// did: resolved all sub-lights with the same sin-heavy formulas.
func cpuCampfireResolution(sc *scene.Scene, t float64) time.Duration {
	start := time.Now()
	for i := range sc.Campfires {
		fr := &sc.Campfires[i]
		for j := 0; j < 3; j++ {
			_, _ = fr.LightAt(j, t)
		}
	}
	return time.Since(start)
}

func BenchmarkCPUCampfireResolution_3(b *testing.B) {
	sc := &scene.Scene{
		Campfires: []scene.Campfire{
			{Center: vec.New(0, 0.1, 0), Color: vec.New(1, 0.6, 0.3),
				Range: 3, Brightness: 5, Jitter: 0.02, Flicker: 0.4, Speed: 1, Seed: 0},
			{Center: vec.New(5, 0.1, 3), Color: vec.New(1, 0.7, 0.4),
				Range: 4, Brightness: 6, Jitter: 0.02, Flicker: 0.4, Speed: 1, Seed: 1},
			{Center: vec.New(-3, 0.1, 2), Color: vec.New(1, 0.65, 0.35),
				Range: 3.5, Brightness: 5.5, Jitter: 0.025, Flicker: 0.35, Speed: 1.1, Seed: 2},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cpuCampfireResolution(sc, float64(i%60))
	}
}

// oldGPUCampfire represents the old 128-byte structure for struct-size comparison.
type oldGPUCampfire struct {
	Core  [4]float32
	Extra [4]float32
	S0Pos [4]float32
	S0Col [4]float32
	S1Pos [4]float32
	S1Col [4]float32
	S2Pos [4]float32
	S2Col [4]float32
}

func TestCampfireStructSize(t *testing.T) {
	oldSize := int(unsafe.Sizeof(oldGPUCampfire{}))
	newSize := int(unsafe.Sizeof(CampfireParams{}))
	t.Logf("old GPUCampfire: %d bytes", oldSize)
	t.Logf("new CampfireParams: %d bytes", newSize)
	t.Logf("reduction: %.0f%%", float64(oldSize-newSize)/float64(oldSize)*100)
}
