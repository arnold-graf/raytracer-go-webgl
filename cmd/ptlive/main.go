// Command ptlive is an interactive viewer for the experimental path tracer.
//
// It is a free-fly camera over the scene — no physics, no NPCs, no doors — so
// it can drive internal/webgpu.PathTracer directly without going through the
// game's own loop. The image accumulates while you hold still and resets the
// moment the camera moves, which is the whole point: it shows exactly how many
// samples per second this machine can put into a frame, and how long a still
// frame takes to become clean.
//
// Controls:
//
//	click        capture the mouse (ESC releases)
//	WASD         move, mouse look
//	Q / E        down / up
//	shift        move faster
//	space        pause accumulation (freeze the current image)
//	R            reset the accumulator
//	1            toggle the scene's ambient constant
//	2            toggle next-event estimation
//	[ / ]        fewer / more samples per displayed frame
//
// Usage:
//
//	go run ./cmd/ptlive -scene scenes/office-sunset/index.toml
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
	"raytracer/internal/webgpu"
)

type game struct {
	pt   *webgpu.PathTracer
	cam  *camera.Camera
	view *render.View

	w, h  int
	buf   []byte
	frame *ebiten.Image

	locked         bool
	prevCX, prevCY int
	paused         bool
	sppPerFrame    int
	speed          float64

	// Accumulation is only valid while the camera holds still, so the pose it
	// was started from is kept and compared every frame.
	lastPos            vec.V
	lastYaw, lastPitch float64
	traceTime          time.Duration
	tracedSamples      int
	lastFrameGPU       time.Duration
	sinceReset         time.Time
	convergedAnnounced bool
}

func main() {
	scenePath := flag.String("scene", "scenes/office-sunset/index.toml", "TOML scene to view")
	width := flag.Int("w", 512, "render width")
	height := flag.Int("h", 320, "render height")
	scale := flag.Int("scale", 2, "window scale")
	depth := flag.Uint("depth", 8, "maximum path length")
	spf := flag.Int("spp-frame", 1, "samples per displayed frame")
	ambient := flag.Bool("ambient", false, "start with the scene's ambient constant restored")
	physical := flag.Bool("physical", false, "inverse-square light falloff")
	denoise := flag.Bool("denoise", true, "spatial a-trous reconstruction on top of temporal reprojection")
	temporal := flag.Bool("temporal", true, "temporal reprojection (keeps grain and sharpness; only averages a pixel with its own history)")
	atrousFade := flag.Float64("atrous-fade", 24, "history length at which spatial filtering stops (0 = always filter)")
	yawDeg := flag.Float64("yaw-deg", 0, "override starting camera yaw in degrees")
	flag.Parse()

	sc, err := sceneio.Load(*scenePath)
	if err != nil {
		log.Fatal(err)
	}
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	if *yawDeg != 0 {
		cam.Yaw = *yawDeg * math.Pi / 180
	}

	r, err := webgpu.New(*width, *height)
	if err != nil {
		log.Fatalf("webgpu unavailable: %v", err)
	}
	defer r.Release()

	aoData, aoOK := probe.New(sc).BakeAO()
	view := &render.View{
		Scene: sc, Shadow: true, Mirror: true, AO: true,
		AOData: aoData, AOok: aoOK, AdaptiveAA: true, MaxBounceDepth: 4,
	}

	opts := webgpu.DefaultPTOptions()
	opts.MaxDepth = uint32(*depth)
	opts.Ambient = *ambient
	opts.Physical = *physical
	opts.Temporal = *temporal
	opts.Atrous = *denoise
	opts.AtrousFade = float32(*atrousFade)
	pt, err := webgpu.NewPathTracer(r, opts)
	if err != nil {
		log.Fatalf("path tracer: %v", err)
	}
	defer pt.Release()

	g := &game{
		pt: pt, cam: cam, view: view,
		w: *width, h: *height,
		buf:         make([]byte, (*width)*(*height)*4),
		frame:       ebiten.NewImage(*width, *height),
		sppPerFrame: *spf,
		speed:       0.35,
		sinceReset:  time.Now(),
	}
	if err := pt.Reset(cam, view); err != nil {
		log.Fatalf("reset: %v", err)
	}
	g.markPose()

	ebiten.SetWindowSize((*width)*(*scale), (*height)*(*scale))
	ebiten.SetWindowTitle("Path tracer (experimental) — " + *scenePath)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	// Without this Ebiten throttles Update to 60 Hz; a path traced frame is far
	// slower than that and should run as fast as it can.
	ebiten.SetVsyncEnabled(false)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}

func (g *game) markPose() {
	g.lastPos, g.lastYaw, g.lastPitch = g.cam.Pos, g.cam.Yaw, g.cam.Pitch
}

func (g *game) moved() bool {
	d := g.cam.Pos.Sub(g.lastPos)
	return math.Abs(d.X)+math.Abs(d.Y)+math.Abs(d.Z) > 1e-6 ||
		g.cam.Yaw != g.lastYaw || g.cam.Pitch != g.lastPitch
}

func (g *game) Update() error {
	if !g.locked && inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		ebiten.SetCursorMode(ebiten.CursorModeCaptured)
		g.prevCX, g.prevCY = ebiten.CursorPosition()
		g.locked = true
	}
	if g.locked && inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
		g.locked = false
	}
	if g.locked {
		cx, cy := ebiten.CursorPosition()
		g.cam.Look(float64(cx-g.prevCX), float64(cy-g.prevCY))
		g.prevCX, g.prevCY = cx, cy
	}

	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.paused = !g.paused
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.reset()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit1) {
		o := g.pt.Options()
		o.Ambient = !o.Ambient
		g.pt.SetOptions(o)
		g.reset()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit2) {
		o := g.pt.Options()
		o.NoNEE = !o.NoNEE
		g.pt.SetOptions(o)
		g.reset()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit3) {
		// Cycles off -> temporal only -> temporal + spatial. The middle stop
		// is the one worth knowing about: reprojection alone never blurs
		// anything, it only averages a pixel with its own history, so it keeps
		// the raw estimator's grain and sharpness and just gets there sooner.
		o := g.pt.Options()
		switch {
		case !o.Temporal && !o.Atrous:
			o.Temporal, o.Atrous = true, false
		case o.Temporal && !o.Atrous:
			o.Temporal, o.Atrous = true, true
		default:
			o.Temporal, o.Atrous = false, false
		}
		g.pt.SetOptions(o)
		g.reset()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyBracketLeft) && g.sppPerFrame > 1 {
		g.sppPerFrame--
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyBracketRight) && g.sppPerFrame < 32 {
		g.sppPerFrame++
	}

	g.fly()

	if g.moved() {
		// With reprojection the history survives camera motion; only the
		// progressive sample counter restarts, so the HUD keeps meaning
		// something. Without it there is nothing to reproject and the
		// accumulator has to be cleared.
		o := g.pt.Options()
		if o.Temporal {
			g.markPose()
			g.tracedSamples = 0
			g.traceTime = 0
		} else {
			g.reset()
		}
	}
	if !g.paused {
		if err := g.pt.Accumulate(g.cam, g.view, uint32(g.sppPerFrame)); err != nil {
			return err
		}
		g.lastFrameGPU = g.pt.GPUTime()
		g.traceTime += g.lastFrameGPU
		g.tracedSamples += g.sppPerFrame
	}
	return g.pt.Resolve(g.buf)
}

func (g *game) reset() {
	if err := g.pt.Reset(g.cam, g.view); err != nil {
		log.Printf("reset: %v", err)
	}
	g.markPose()
	g.traceTime = 0
	g.tracedSamples = 0
	g.sinceReset = time.Now()
	g.convergedAnnounced = false
}

// fly moves the camera along its own basis. Deliberately not camera.Update:
// this viewer has no world to collide against, and flying through the geometry
// is the fastest way to find a view worth looking at.
func (g *game) fly() {
	fwd, right, up := g.cam.Basis()
	step := g.speed
	if ebiten.IsKeyPressed(ebiten.KeyShift) {
		step *= 4
	}
	var d vec.V
	if ebiten.IsKeyPressed(ebiten.KeyW) {
		d = d.Add(fwd)
	}
	if ebiten.IsKeyPressed(ebiten.KeyS) {
		d = d.Sub(fwd)
	}
	if ebiten.IsKeyPressed(ebiten.KeyD) {
		d = d.Add(right)
	}
	if ebiten.IsKeyPressed(ebiten.KeyA) {
		d = d.Sub(right)
	}
	if ebiten.IsKeyPressed(ebiten.KeyE) {
		d = d.Add(up)
	}
	if ebiten.IsKeyPressed(ebiten.KeyQ) {
		d = d.Sub(up)
	}
	if d.X != 0 || d.Y != 0 || d.Z != 0 {
		g.cam.Pos = g.cam.Pos.Add(d.Normalize().Scale(step))
	}
}

func (g *game) Draw(screen *ebiten.Image) {
	g.frame.WritePixels(g.buf)
	screen.DrawImage(g.frame, nil)

	spp := g.pt.Samples()
	msPerSPP := 0.0
	if g.tracedSamples > 0 {
		msPerSPP = float64(g.traceTime.Microseconds()) / 1000.0 / float64(g.tracedSamples)
	}
	o := g.pt.Options()
	state := ""
	if g.paused {
		state = "  PAUSED"
	}
	lines := fmt.Sprintf("%d spp   %.0f ms/spp   %.1f fps   %d spp/frame%s",
		spp, msPerSPP, 1000.0/math.Max(float64(g.lastFrameGPU.Milliseconds()), 1), g.sppPerFrame, state)
	mode := "off"
	if o.Temporal && o.Atrous {
		mode = "temporal+spatial"
	} else if o.Temporal {
		mode = "temporal"
	}
	opts := fmt.Sprintf("[1] ambient %v   [2] NEE %v   [3] reconstruct %s   depth %d",
		o.Ambient, !o.NoNEE, mode, o.MaxDepth)
	drawLabel(screen, lines, 4, 4)
	drawLabel(screen, opts, 4, 16)
	if !g.locked {
		drawLabel(screen, "click to capture mouse — WASD/QE fly, shift fast, R reset", 4, g.h-14)
	}
}

// drawLabel renders one HUD line with the stdlib bitmap font. Cached per
// string, because the two lines only change when a counter ticks.
var labelCache = map[string]*ebiten.Image{}

func drawLabel(dst *ebiten.Image, s string, x, y int) {
	img, ok := labelCache[s]
	if !ok {
		face := basicfont.Face7x13
		w := font.MeasureString(face, s).Ceil()
		h := face.Metrics().Height.Ceil()
		if w <= 0 || h <= 0 {
			return
		}
		rgba := image.NewRGBA(image.Rect(0, 0, w, h))
		d := &font.Drawer{
			Dst:  rgba,
			Src:  image.NewUniform(color.RGBA{255, 240, 180, 255}),
			Face: face,
			Dot:  fixed.P(0, face.Metrics().Ascent.Ceil()),
		}
		d.DrawString(s)
		img = ebiten.NewImageFromImage(rgba)
		if len(labelCache) > 512 {
			labelCache = map[string]*ebiten.Image{}
		}
		labelCache[s] = img
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(x), float64(y))
	dst.DrawImage(img, op)
}

func (g *game) Layout(int, int) (int, int) { return g.w, g.h }
