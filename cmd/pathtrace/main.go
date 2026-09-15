// Command pathtrace renders a scene with the experimental Monte Carlo path
// tracer (internal/webgpu/pathtrace.go) instead of the shipping megakernel.
//
// It shares the megakernel's geometry — same BVH, same analytic primitives,
// same procedural textures and sky — and replaces the entire light transport
// model: no hemispheric ambient, no baked AO, no screen-space penumbra filter,
// no adaptive AA, no ray-segment stack. Indirect light, contact shadows,
// penumbrae and antialiasing all come out of sampling instead.
//
// Usage:
//
//	go run ./cmd/pathtrace -scene scenes/office-sunset/index.toml -spp 256 -o tmp/pt.png
//	go run ./cmd/pathtrace -scene scenes/outdoors-night-villa.toml -spp 512 -compare -o tmp/villa.png
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/webgpu"
)

func main() {
	scenePath := flag.String("scene", "scenes/office-sunset/index.toml", "TOML scene to render")
	width := flag.Int("w", 512, "render width")
	height := flag.Int("h", 320, "render height")
	spp := flag.Int("spp", 128, "total samples per pixel")
	batch := flag.Int("batch", 0, "samples per dispatch (0 = auto-size to -dispatch-ms)")
	dispatchMS := flag.Int("dispatch-ms", 250, "target wall time per dispatch; the batch is grown to fit")
	depth := flag.Uint("depth", 8, "maximum path length")
	rr := flag.Uint("rr", 3, "path length at which Russian roulette starts")
	out := flag.String("o", "tmp/pathtrace.png", "output PNG")
	compare := flag.Bool("compare", false, "also render the same frame with the megakernel, to <out>-mega.png")
	yawDeg := flag.Float64("yaw-deg", 0, "override camera yaw in degrees")
	pitchDeg := flag.Float64("pitch-deg", 0, "override camera pitch in degrees")
	camX := flag.Float64("cam-x", 0, "override camera X")
	camY := flag.Float64("cam-y", 0, "override camera Y")
	camZ := flag.Float64("cam-z", 0, "override camera Z")
	clock := flag.Float64("time", 0, "animation clock in seconds")
	physical := flag.Bool("physical", false, "inverse-square light falloff instead of the scene-authored curve")
	noNEE := flag.Bool("no-nee", false, "disable next-event estimation (brute force; much noisier)")
	noSky := flag.Bool("no-sky", false, "treat the sky as black (isolates local lighting)")
	lightScale := flag.Float64("light-scale", math.Pi, "scales direct lighting; pi matches the megakernel's brightness")
	clampInd := flag.Float64("clamp", 12, "firefly clamp on indirect contributions (0 = off)")
	debug := flag.String("debug", "", "diagnostic view: albedo | normal | direct | indirect")
	lightSamples := flag.Uint("light-samples", 1, "next-event estimation samples per shading vertex")
	noShadow := flag.Bool("no-shadow", false, "ablation: skip next-event occlusion tests")
	restir := flag.Bool("restir", false, "reuse the previous frame's reservoir (needs a sequence of frames: use -batch 1)")
	risCandidates := flag.Int("ris", 16, "emitter resampling candidates per vertex (1 = uniform selection)")
	noEmitterNEE := flag.Bool("no-emitter-nee", false, "ablation: find emissive geometry by BSDF sampling only")
	whiteNoise := flag.Bool("white-noise", false, "ablation: use the old PCG sampler instead of Owen-scrambled Sobol")
	allLights := flag.Bool("all-lights", false, "ablation: ignore the light cluster grid and draw from every light")
	ambient := flag.Bool("ambient", false, "restore the scene's hemispheric ambient constant (A/B; not physical)")
	checkpoints := flag.String("checkpoints", "", "also write the image at these sample counts, e.g. 1,4,16,64")
	temporal := flag.Bool("temporal", false, "temporal reprojection (only useful with a moving camera)")
	denoise := flag.Bool("denoise", false, "edge-aware a-trous reconstruction")
	atrousPasses := flag.Int("atrous-passes", 5, "a-trous iterations")
	phiLum := flag.Float64("phi-lum", 4, "a-trous luminance edge-stop, in standard deviations")
	atrousFade := flag.Float64("atrous-fade", 24, "history length at which spatial filtering stops (0 = always filter)")
	maxHistory := flag.Float64("max-history", 512, "frames the temporal running mean may reach")
	drift := flag.Float64("drift", 0, "move the camera sideways this far per frame; exercises temporal reprojection")
	flag.Parse()

	if *spp <= 0 {
		log.Fatal("spp must be positive")
	}

	sc, err := sceneio.Load(*scenePath)
	if err != nil {
		log.Fatal(err)
	}
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	if isSet("yaw-deg") {
		cam.Yaw = *yawDeg * math.Pi / 180
	}
	if isSet("pitch-deg") {
		cam.Pitch = *pitchDeg * math.Pi / 180
	}
	if isSet("cam-x") {
		cam.Pos.X = *camX
	}
	if isSet("cam-y") {
		cam.Pos.Y = *camY
	}
	if isSet("cam-z") {
		cam.Pos.Z = *camZ
	}

	r, err := webgpu.New(*width, *height)
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
		AdaptiveAA:     true,
		MaxBounceDepth: 4,
	}

	opts := webgpu.DefaultPTOptions()
	opts.MaxDepth = uint32(*depth)
	opts.RRDepth = uint32(*rr)
	opts.Physical = *physical
	opts.NoNEE = *noNEE
	opts.NoSky = *noSky
	opts.LightScale = float32(*lightScale)
	opts.ClampIndirect = float32(*clampInd)
	opts.LightSamples = uint32(*lightSamples)
	opts.NoShadow = *noShadow
	opts.AllLights = *allLights
	opts.WhiteNoise = *whiteNoise
	opts.NoEmitterNEE = *noEmitterNEE
	opts.RISCandidates = uint32(*risCandidates)
	opts.RestirTemporal = *restir
	opts.Ambient = *ambient
	opts.Temporal = *temporal
	opts.Atrous = *denoise
	opts.AtrousPasses = *atrousPasses
	opts.PhiLum = float32(*phiLum)
	opts.AtrousFade = float32(*atrousFade)
	opts.MaxHistory = float32(*maxHistory)
	switch *debug {
	case "":
	case "albedo":
		opts.Debug = webgpu.PTDebugAlbedo
	case "normal":
		opts.Debug = webgpu.PTDebugNormal
	case "direct":
		opts.Debug = webgpu.PTDebugDirect
	case "indirect":
		opts.Debug = webgpu.PTDebugIndirect
	default:
		log.Fatalf("unknown -debug view %q (want albedo, normal, direct or indirect)", *debug)
	}

	pt, err := webgpu.NewPathTracer(r, opts)
	if err != nil {
		log.Fatalf("path tracer: %v", err)
	}
	defer pt.Release()

	fmt.Printf("path tracer: %s  %dx%d  %d spp  depth %d (rr from %d)\n",
		*scenePath, *width, *height, *spp, *depth, *rr)
	falloff := "scene-authored"
	if *physical {
		falloff = "inverse-square"
	}
	fmt.Printf("  falloff %s, NEE %v (%d sample(s)/vertex), sky %v, clamp %.0f\n",
		falloff, !*noNEE, *lightSamples, !*noSky, *clampInd)
	fmt.Printf("  scene: %d lights, %d emissive prims\n\n", len(webgpu.PackLights(sc)), countEmissive(sc))

	if err := pt.Reset(cam, view); err != nil {
		log.Fatalf("reset: %v", err)
	}

	marks := parseCheckpoints(*checkpoints, *spp)
	buf := make([]byte, (*width)*(*height)*4)
	start := time.Now()
	var traceTime time.Duration
	done := 0
	// A dispatch that runs for seconds trips the OS GPU watchdog and comes back
	// as a torn frame, so samples are batched to a wall-time target rather than
	// a fixed count. The villa costs 7x what the office does per sample, which
	// is exactly the kind of spread a fixed batch cannot straddle.
	n := *batch
	if n <= 0 {
		n = 1
	}
	for done < *spp {
		if *batch <= 0 && done > 0 {
			perSPP := float64(traceTime.Microseconds()) / 1000.0 / float64(done)
			if perSPP > 0 {
				n = int(float64(*dispatchMS) / perSPP)
			}
			if n < 1 {
				n = 1
			}
			if n > 64 {
				n = 64
			}
		}
		if next, ok := nextMark(marks, done); ok && done+n > next {
			n = next - done
		}
		if done+n > *spp {
			n = *spp - done
		}
		if err := pt.Accumulate(cam, view, uint32(n)); err != nil {
			log.Fatalf("accumulate: %v", err)
		}
		if *drift != 0 {
			// A still camera never exercises reprojection: every pixel maps to
			// itself and a broken projection looks perfect. Drifting sideways
			// is the cheapest way to make the pass actually do its job.
			_, right, _ := cam.Basis()
			cam.Pos = cam.Pos.Add(right.Scale(*drift))
		}
		traceTime += pt.GPUTime()
		done += n
		if marks[done] {
			if err := pt.Resolve(buf); err != nil {
				log.Fatalf("resolve: %v", err)
			}
			if err := writePNG(suffix(*out, fmt.Sprintf("-%dspp", done)), buf, *width, *height); err != nil {
				log.Fatalf("write png: %v", err)
			}
		}
		fmt.Printf("\r  %4d/%d spp   %6.1fs elapsed   %6.1f ms/spp",
			done, *spp, time.Since(start).Seconds(),
			float64(traceTime.Milliseconds())/float64(done))
	}
	fmt.Println()

	if err := pt.Resolve(buf); err != nil {
		log.Fatalf("resolve: %v", err)
	}
	if err := writePNG(*out, buf, *width, *height); err != nil {
		log.Fatalf("write png: %v", err)
	}

	perSample := float64(traceTime.Microseconds()) / 1000.0 / float64(*spp)
	fmt.Printf("\n  %d spp in %.1fs of GPU time — %.1f ms/spp (%.1f fps at 1 spp)\n",
		*spp, traceTime.Seconds(), perSample, 1000.0/perSample)
	fmt.Printf("  wrote %s\n", *out)

	if *compare {
		megaPath := suffix(*out, "-mega")
		// Warm up, then take the megakernel's own timing for the same camera.
		for i := 0; i < 3; i++ {
			r.Render(buf, cam, view, 1)
		}
		best := time.Duration(math.MaxInt64)
		for i := 0; i < 5; i++ {
			r.Render(buf, cam, view, 1)
			if g := r.LastTiming().GPU; g < best {
				best = g
			}
		}
		if err := writePNG(megaPath, buf, *width, *height); err != nil {
			log.Fatalf("write png: %v", err)
		}
		megaMS := float64(best.Microseconds()) / 1000.0
		fmt.Printf("  megakernel: %.1f ms/frame (%.0f fps) — wrote %s\n", megaMS, 1000.0/megaMS, megaPath)
		fmt.Printf("  path tracer is %.1fx the cost of one megakernel frame, per sample\n", perSample/megaMS)
		sbs := suffix(*out, "-sbs")
		if err := writeSideBySide(sbs, megaPath, *out); err != nil {
			log.Fatalf("side by side: %v", err)
		}
		fmt.Printf("  wrote %s (megakernel left, path tracer right)\n", sbs)
	}
}

// countEmissive reports how many primitives are their own light source. A
// unidirectional path tracer finds these by BSDF sampling only, so a scene with
// many small ones is inherently noisy here.
func countEmissive(sc *scene.Scene) int {
	n := 0
	for _, p := range webgpu.PackPrimitives(sc) {
		if p.Meta[1] == matEmit {
			n++
		}
	}
	return n
}

// matEmit mirrors scene.MatEmit / MAT_EMIT in types.wesl.
const matEmit = 5

// parseCheckpoints turns "1,4,16" into a set of sample counts at which the
// image is written out, so one run produces a convergence ladder.
func parseCheckpoints(spec string, maxSPP int) map[int]bool {
	marks := map[int]bool{}
	if spec == "" {
		return marks
	}
	for _, f := range strings.Split(spec, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil || n <= 0 || n > maxSPP {
			log.Fatalf("bad checkpoint %q (want a positive integer <= %d)", f, maxSPP)
		}
		marks[n] = true
	}
	return marks
}

// nextMark is the smallest checkpoint still ahead of done, so a batch can be
// shortened to land exactly on it.
func nextMark(marks map[int]bool, done int) (int, bool) {
	best, ok := 0, false
	for m := range marks {
		if m > done && (!ok || m < best) {
			best, ok = m, true
		}
	}
	return best, ok
}

// writeSideBySide stitches two equal-size PNGs into one image with a divider,
// which is the only way a noise-versus-filter comparison reads at this size.
func writeSideBySide(dst, left, right string) error {
	li, err := readPNG(left)
	if err != nil {
		return err
	}
	ri, err := readPNG(right)
	if err != nil {
		return err
	}
	lb, rb := li.Bounds(), ri.Bounds()
	const gap = 4
	w := lb.Dx() + gap + rb.Dx()
	h := lb.Dy()
	if rb.Dy() > h {
		h = rb.Dy()
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, image.Rect(0, 0, w, h), &image.Uniform{color.NRGBA{20, 20, 22, 255}}, image.Point{}, draw.Src)
	draw.Draw(out, lb, li, lb.Min, draw.Src)
	draw.Draw(out, image.Rect(lb.Dx()+gap, 0, lb.Dx()+gap+rb.Dx(), rb.Dy()), ri, rb.Min, draw.Src)
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, out)
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func writePNG(path string, rgba []byte, w, h int) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			img.SetNRGBA(x, y, color.NRGBA{R: rgba[i], G: rgba[i+1], B: rgba[i+2], A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func suffix(path, s string) string {
	ext := filepath.Ext(path)
	return path[:len(path)-len(ext)] + s + ext
}

func isSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
