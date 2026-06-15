// Command gpuprof benchmarks the WebGPU renderer headlessly: per-frame phase
// breakdown (pack / upload / GPU / readback) and a feature ablation matrix.
//
// Usage:
//
//	go run ./cmd/gpuprof -scene scenes/manhattan_city_block.toml
//	go run ./cmd/gpuprof -scene scenes/indoor-outdoor.toml -warmup 5 -frames 30
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/sceneio"
	"raytracer/internal/trace"
	"raytracer/internal/webgpu"
)

const (
	renderW = 400
	renderH = 250
)

func main() {
	scenePath := flag.String("scene", "scenes/manhattan_city_block.toml", "TOML scene to profile")
	warmup := flag.Int("warmup", 3, "frames to discard before measuring")
	frames := flag.Int("frames", 20, "measured frames per configuration")
	ablate := flag.Bool("ablate", true, "run the feature ablation matrix after the baseline")
	flag.Parse()

	sc, err := sceneio.Load(*scenePath)
	if err != nil {
		log.Fatal(err)
	}
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}

	r, err := webgpu.New(renderW, renderH)
	if err != nil {
		log.Fatalf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	tr := trace.New(sc)
	tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	tr.Prepare()

	buf := make([]byte, renderW*renderH*4)

	fmt.Printf("GPU profile: %s  (%dx%d)\n\n", *scenePath, renderW, renderH)

	// Baseline at the scene's authored camera.
	base := bench(r, buf, cam, tr, *warmup, *frames)
	printTiming("baseline (scene camera)", base)

	if *ablate {
		fmt.Println()
		fmt.Println("Feature ablation (same camera, toggling one option at a time):")
		fmt.Println()
		fmt.Printf("%-22s  %6s  %6s  %6s  %6s  %6s  %6s\n",
			"config", "pack", "upload", "gpu", "read", "total", "fps")
		fmt.Println(strings.Repeat("-", 72))

		configs := []struct {
			name string
			opts trace.Options
		}{
			{"all on", trace.Options{Mirror: true, Shadow: true, AO: true}},
			{"mirror off", trace.Options{Mirror: false, Shadow: true, AO: true}},
			{"shadow off", trace.Options{Mirror: true, Shadow: false, AO: true}},
			{"AO off", trace.Options{Mirror: true, Shadow: true, AO: false}},
			{"all off", trace.Options{}},
		}
		for _, c := range configs {
			tr.Opts = c.opts
			if c.opts.AO {
				tr.Prepare()
			}
			t := bench(r, buf, cam, tr, *warmup, *frames)
			fmt.Printf("%-22s  %5.1fms %5.1fms %5.1fms %5.1fms %5.1fms %6.0f\n",
				c.name,
				ms(t.Pack), ms(t.Upload), ms(t.GPU), ms(t.Readback), ms(t.Total),
				1000.0/ms(t.Total))
		}
	}

	// Restore defaults and print scene-size context from the last frame.
	tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	tr.Prepare()
	r.Render(buf, cam, tr, 1)
	last := r.LastTiming()
	fmt.Println()
	fmt.Printf("Scene on GPU: %d prims, %d blockers, %d BVH nodes, %d holes\n",
		last.Prims, last.Blockers, last.BVHNodes, last.Holes)
	fmt.Println()
	printNotes()
}

func bench(r *webgpu.Renderer, buf []byte, cam *camera.Camera, tr *trace.Tracer, warmup, n int) webgpu.FrameTiming {
	for i := 0; i < warmup; i++ {
		r.Render(buf, cam, tr, 1)
	}
	var acc webgpu.FrameTiming
	for i := 0; i < n; i++ {
		r.Render(buf, cam, tr, 1)
		t := r.LastTiming()
		acc.Pack += t.Pack
		acc.Upload += t.Upload
		acc.GPU += t.GPU
		acc.Readback += t.Readback
		acc.Total += t.Total
		acc.Prims = t.Prims
		acc.Blockers = t.Blockers
		acc.BVHNodes = t.BVHNodes
		acc.Holes = t.Holes
	}
	d := float64(n)
	return webgpu.FrameTiming{
		Pack:     time.Duration(float64(acc.Pack) / d),
		Upload:   time.Duration(float64(acc.Upload) / d),
		GPU:      time.Duration(float64(acc.GPU) / d),
		Readback: time.Duration(float64(acc.Readback) / d),
		Total:    time.Duration(float64(acc.Total) / d),
		Prims:    acc.Prims,
		Blockers: acc.Blockers,
		BVHNodes: acc.BVHNodes,
		Holes:    acc.Holes,
	}
}

func printTiming(label string, t webgpu.FrameTiming) {
	fmt.Printf("%s\n", label)
	fmt.Printf("  pack     %5.1f ms  (CPU: scene pack + BVH build)\n", ms(t.Pack))
	fmt.Printf("  upload   %5.1f ms  (CPU: WriteBuffer every frame)\n", ms(t.Upload))
	fmt.Printf("  gpu      %5.1f ms  (compute dispatch, wall until idle)\n", ms(t.GPU))
	fmt.Printf("  readback %5.1f ms  (map + copy output buffer)\n", ms(t.Readback))
	fmt.Printf("  total    %5.1f ms  (~%.0f fps)\n", ms(t.Total), 1000.0/ms(t.Total))
	fmt.Printf("  scene: %d prims, %d blockers, %d BVH nodes, %d holes\n",
		t.Prims, t.Blockers, t.BVHNodes, t.Holes)
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func printNotes() {
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  • GPU time is wall-clock until the device is idle (Poll), so it includes")
	fmt.Fprintln(os.Stderr, "    the compute pass but not overlapped presentation.")
	fmt.Fprintln(os.Stderr, "  • Pack+Upload repeat every frame even for static scenes — a future cache")
	fmt.Fprintln(os.Stderr, "    would reclaim that CPU time.")
	fmt.Fprintln(os.Stderr, "  • In-game: run with -renderer webgpu; the HUD shows the same breakdown.")
}
