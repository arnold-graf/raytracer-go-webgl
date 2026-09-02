// Command gpuprof benchmarks the WebGPU renderer headlessly: per-frame phase
// breakdown (pack / upload / GPU / readback), optional GPU shader counters,
// and a feature ablation matrix.
//
// Usage:
//
//	go run ./cmd/gpuprof -scene scenes/outdoors-night-villa.toml
//	go run ./cmd/gpuprof -scene scenes/outdoors-night-villa.toml -profile
//	go run ./cmd/gpuprof -scene scenes/indoor-outdoor.toml -warmup 5 -frames 30
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/webgpu"
)

// Defaults mirror the shipping app so a profile run measures the workload the
// player actually sees. main.go renders at 512x320, and app.go builds its
// render.View with MaxBounceDepth 4 and adaptiveAA on. Profiling against
// anything cheaper (the old 400x250 / depth-2 / AA-off defaults) understates
// glass-heavy scenes badly: bounce depth drives the ray tree exponentially, so
// depth 2 hid most of the cost this tool exists to find.
const (
	defaultRenderW     = 512
	defaultRenderH     = 320
	defaultBounceDepth = 4
)

func main() {
	scenePath := flag.String("scene", "scenes/manhattan_city_block.toml", "TOML scene to profile")
	width := flag.Int("w", defaultRenderW, "render width")
	height := flag.Int("h", defaultRenderH, "render height")
	yawDeg := flag.Float64("yaw-deg", 0, "override camera yaw in degrees")
	pitchDeg := flag.Float64("pitch-deg", 0, "override camera pitch in degrees")
	camX := flag.Float64("cam-x", 0, "override camera X (0 = scene default)")
	camY := flag.Float64("cam-y", 0, "override camera Y (0 = scene default)")
	camZ := flag.Float64("cam-z", 0, "override camera Z (0 = scene default)")
	warmup := flag.Int("warmup", 3, "frames to discard before measuring")
	frames := flag.Int("frames", 20, "measured frames per configuration")
	ablate := flag.Bool("ablate", true, "run the feature ablation matrix after the baseline")
	profile := flag.Bool("profile", false, "collect GPU shader workload counters (one profiled frame)")
	dump := flag.String("dump", "", "write the final RGBA frame buffer to this file (for A/B pixel diffs)")
	mountains := flag.Bool("mountains", false, "use mountain-view camera preset (yaw=0°, villa valley view)")
	aa := flag.Bool("aa", true, "enable adaptive anti-aliasing (two-pass), as the app does")
	depth := flag.Uint("depth", defaultBounceDepth, "max mirror/glass bounce depth (app uses 4; 0 = shader default of 2)")
	clock := flag.Float64("time", 0, "animation clock in seconds (campfire sub-lights, flames, water ripples)")
	flag.Parse()

	renderW, renderH := *width, *height
	if renderW <= 0 || renderH <= 0 {
		log.Fatalf("invalid render size %dx%d", renderW, renderH)
	}

	sc, err := sceneio.Load(*scenePath)
	if err != nil {
		log.Fatal(err)
	}
	cam := camera.New()
	camLabel := "default camera"
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
		camLabel = fmt.Sprintf("scene camera (yaw=%.0f° pitch=%.1f°)", sc.Start.Yaw*180/math.Pi, sc.Start.Pitch*180/math.Pi)
	}
	if *mountains {
		cam.Yaw = 0
		cam.Pitch = 0.04
		camLabel = "mountain view preset (yaw=0° pitch=2.3°)"
	}
	if flagPassed("yaw-deg") {
		cam.Yaw = *yawDeg * math.Pi / 180
	}
	if flagPassed("pitch-deg") {
		cam.Pitch = *pitchDeg * math.Pi / 180
	}
	if flagPassed("cam-x") || flagPassed("cam-y") || flagPassed("cam-z") {
		if flagPassed("cam-x") {
			cam.Pos.X = *camX
		}
		if flagPassed("cam-y") {
			cam.Pos.Y = *camY
		}
		if flagPassed("cam-z") {
			cam.Pos.Z = *camZ
		}
		camLabel = fmt.Sprintf("pos=[%.1f,%.1f,%.1f] yaw=%.0f° pitch=%.1f°",
			cam.Pos.X, cam.Pos.Y, cam.Pos.Z, cam.Yaw*180/math.Pi, cam.Pitch*180/math.Pi)
	}
	if flagPassed("yaw-deg") || flagPassed("pitch-deg") {
		camLabel = fmt.Sprintf("yaw=%.0f° pitch=%.1f°", cam.Yaw*180/math.Pi, cam.Pitch*180/math.Pi)
	}

	r, err := webgpu.New(renderW, renderH)
	if err != nil {
		log.Fatalf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	aoData, aoOK := probe.New(sc).BakeAO()
	view := &render.View{
		Scene:          sc,
		Time:           *clock,
		Shadow:         true,
		Mirror:         true,
		AO:             true,
		AOData:         aoData,
		AOok:           aoOK,
		AdaptiveAA:     *aa,
		MaxBounceDepth: uint32(*depth),
	}

	buf := make([]byte, renderW*renderH*4)

	fmt.Printf("GPU profile: %s  (%dx%d)  %s\n", *scenePath, renderW, renderH, camLabel)
	fmt.Printf("  bounce depth %d, adaptive AA %v%s\n\n", *depth, *aa, appConfigNote(renderW, renderH, *depth, *aa))
	printSceneContext(sc)

	// Baseline at the configured camera.
	base := bench(r, buf, cam, view, *warmup, *frames, false)
	printTiming("baseline", base)

	if *dump != "" {
		r.Render(buf, cam, view, 1)
		if err := os.WriteFile(*dump, buf, 0o644); err != nil {
			log.Fatalf("dump frame: %v", err)
		}
		fmt.Printf("dumped frame to %s (%d bytes)\n", *dump, len(buf))
	}

	if *profile {
		fmt.Println()
		fmt.Println("Shader counters (single profiled frame, counters add ~5-15%% GPU overhead):")
		fmt.Println()
		r.SetProfiling(true)
		for i := 0; i < *warmup; i++ {
			r.Render(buf, cam, view, 1)
		}
		r.Render(buf, cam, view, 1)
		r.SetProfiling(false)
		t := r.LastTiming()
		fmt.Print(webgpu.FormatGPUProfile(r.LastGPUProfile(), float64(t.GPU)/float64(time.Millisecond)))
	}

	if *ablate {
		fmt.Println()
		fmt.Println("Feature ablation (same camera, toggling one option at a time):")
		fmt.Println()
		fmt.Printf("%-22s  %6s  %6s  %6s  %6s  %6s  %6s\n",
			"config", "pack", "upload", "gpu", "read", "total", "fps")
		fmt.Println(strings.Repeat("-", 72))

		configs := []struct {
			name                   string
			mirror, shadow, aoFlag bool
		}{
			{"all on", true, true, true},
			{"mirror off", false, true, true},
			{"shadow off", true, false, true},
			{"AO off", true, true, false},
			{"all off", false, false, false},
		}
		for _, c := range configs {
			view.Mirror, view.Shadow, view.AO = c.mirror, c.shadow, c.aoFlag
			t := bench(r, buf, cam, view, *warmup, *frames, false)
			fmt.Printf("%-22s  %5.1fms %5.1fms %5.1fms %5.1fms %5.1fms %6.0f\n",
				c.name,
				ms(t.Pack), ms(t.Upload), ms(t.GPU), ms(t.Readback), ms(t.Total),
				1000.0/ms(t.Total))
		}
	}

	// Restore defaults and print scene-size context from the last frame.
	view.Mirror, view.Shadow, view.AO = true, true, true
	r.Render(buf, cam, view, 1)
	last := r.LastTiming()
	fmt.Println()
	fmt.Printf("Scene on GPU: %d prims, %d blockers, %d BVH nodes, %d holes\n",
		last.Prims, last.Blockers, last.BVHNodes, last.Holes)
	fmt.Println()
	printNotes()
}

func printSceneContext(s *scene.Scene) {
	if s == nil {
		return
	}
	if cat := s.Instancing(); cat != nil && len(cat.Placements) > 0 {
		fmt.Printf("Instancing: %d placements, %d templates", len(cat.Placements), len(cat.Templates))
		if s.HasInstancing() {
			fmt.Print(" (GPU TLAS/BLAS)")
		}
		fmt.Println()
	}
	if n := len(s.Terrains); n > 0 {
		fmt.Printf("Terrain: %d volume(s)\n", n)
	}
	fmt.Println()
}

// appConfigNote flags a run that no longer matches the shipping app, so numbers
// from a deliberately cheapened config are never mistaken for player-visible
// frame times.
func appConfigNote(w, h int, depth uint, aa bool) string {
	if w == defaultRenderW && h == defaultRenderH && depth == defaultBounceDepth && aa {
		return "  (matches the app)"
	}
	return fmt.Sprintf("  (app renders %dx%d at depth %d with AA on)",
		defaultRenderW, defaultRenderH, defaultBounceDepth)
}

func bench(r *webgpu.Renderer, buf []byte, cam *camera.Camera, view *render.View, warmup, n int, profile bool) webgpu.FrameTiming {
	r.SetProfiling(profile)
	for i := 0; i < warmup; i++ {
		r.Render(buf, cam, view, 1)
	}
	var acc webgpu.FrameTiming
	for i := 0; i < n; i++ {
		r.Render(buf, cam, view, 1)
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
	r.SetProfiling(false)
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

func flagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func printNotes() {
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  • GPU time is wall-clock until the device is idle (Poll), so it includes")
	fmt.Fprintln(os.Stderr, "    the compute pass but not overlapped presentation.")
	fmt.Fprintln(os.Stderr, "  • Pack+Upload are near-zero for static scenes: the scene cache packs and")
	fmt.Fprintln(os.Stderr, "    uploads geometry once and re-sends only when the scene changes.")
	fmt.Fprintln(os.Stderr, "  • Use -profile for shader workload counters; -mountains for the slow villa view.")
	fmt.Fprintln(os.Stderr, "  • In-game HUD [0]: gpu ms budget + live workload counters (paths/shadows/terrain/bounces).")
}
