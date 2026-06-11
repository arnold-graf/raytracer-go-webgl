// Package app wires the renderer to a window using Ebiten: it owns the game
// loop, translates keyboard/mouse input into camera motion, manages relative
// mouse capture (the analog of the browser's pointer lock), and draws the HUD.
package app

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/trace"
)

// Game is the Ebiten game implementing the render loop and input handling.
type Game struct {
	rw, rh int

	ren *render.Renderer
	cam *camera.Camera
	tr  *trace.Tracer

	buf   []byte
	frame *ebiten.Image

	pixSize int
	locked  bool
	prevCX  int
	prevCY  int
	elapsed float64 // animation clock in seconds
}

// New builds a game with the given internal render resolution rendering the
// provided scene, using cfg for player-movement tuning.
func New(rw, rh int, sc *scene.Scene, cfg camera.Config) *Game {
	g := &Game{
		rw:      rw,
		rh:      rh,
		ren:     render.New(rw, rh),
		cam:     camera.New(),
		tr:      trace.New(sc),
		buf:     make([]byte, rw*rh*4),
		frame:   ebiten.NewImage(rw, rh),
		pixSize: 1,
	}
	g.tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	g.tr.Prepare() // bake the AO volume now so the first frame doesn't stall
	g.cam.SetConfig(cfg)
	g.cam.SetWorld(sc)
	if sc.Start.Set {
		g.cam.Pos, g.cam.Yaw, g.cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	g.cam.SnapToGround()
	return g
}

// Update advances input and camera state once per tick (60 Hz).
func (g *Game) Update() error {
	g.handleCapture()
	g.handleToggles()

	// Mouse look while captured (relative motion, like pointer lock).
	if g.locked {
		cx, cy := ebiten.CursorPosition()
		g.cam.Look(float64(cx-g.prevCX), float64(cy-g.prevCY))
		g.prevCX, g.prevCY = cx, cy
	}

	// Fixed-step dt matching the original (clamped to 0.1 of a 60 Hz frame).
	const dt = 0.1
	g.cam.Update(camera.Move{
		Forward: ebiten.IsKeyPressed(ebiten.KeyW) || ebiten.IsKeyPressed(ebiten.KeyArrowUp),
		Back:    ebiten.IsKeyPressed(ebiten.KeyS) || ebiten.IsKeyPressed(ebiten.KeyArrowDown),
		Left:    ebiten.IsKeyPressed(ebiten.KeyA) || ebiten.IsKeyPressed(ebiten.KeyArrowLeft),
		Right:   ebiten.IsKeyPressed(ebiten.KeyD) || ebiten.IsKeyPressed(ebiten.KeyArrowRight),
		Jump:    ebiten.IsKeyPressed(ebiten.KeySpace),
	}, dt)

	// Advance the animation clock (Update runs at a fixed 60 Hz).
	g.elapsed += 1.0 / 60.0
	g.tr.Time = g.elapsed

	return nil
}

func (g *Game) handleCapture() {
	if !g.locked && inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		ebiten.SetCursorMode(ebiten.CursorModeCaptured)
		g.prevCX, g.prevCY = ebiten.CursorPosition()
		g.locked = true
	}
	if g.locked && inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
		g.locked = false
	}
}

func (g *Game) handleToggles() {
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit1) {
		g.tr.Opts.Mirror = !g.tr.Opts.Mirror
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit2) {
		g.tr.Opts.Shadow = !g.tr.Opts.Shadow
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit3) {
		g.tr.Opts.AO = !g.tr.Opts.AO
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit4) {
		g.cam.NoClip = !g.cam.NoClip
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyMinus) || inpututil.IsKeyJustPressed(ebiten.KeyLeftBracket) {
		if g.pixSize < 8 {
			g.pixSize++
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEqual) || inpututil.IsKeyJustPressed(ebiten.KeyRightBracket) {
		if g.pixSize > 1 {
			g.pixSize--
		}
	}
}

// Draw raytraces the scene into the framebuffer and blits it with the HUD.
func (g *Game) Draw(screen *ebiten.Image) {
	g.ren.Render(g.buf, g.cam, g.tr, g.pixSize)
	g.frame.WritePixels(g.buf)
	screen.DrawImage(g.frame, nil)

	hud := fmt.Sprintf("%.0f fps  |  %s", ebiten.ActualFPS(), g.statusLine())
	ebitenutil.DebugPrintAt(screen, hud, 4, 4)
	ebitenutil.DebugPrintAt(screen, g.helpLine(), 4, g.rh-14)
}

func (g *Game) statusLine() string {
	if g.locked {
		return fmt.Sprintf("mirror[1]:%s shadow[2]:%s AO[3]:%s noclip[4]:%s px[-/+]:%d  ESC release",
			onOff(g.tr.Opts.Mirror), onOff(g.tr.Opts.Shadow), onOff(g.tr.Opts.AO), onOff(g.cam.NoClip), g.pixSize)
	}
	return "click to capture mouse"
}

func (g *Game) helpLine() string {
	return "WASD/arrows move   mouse look   Space jump"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// Layout fixes the logical screen to the internal render resolution; Ebiten
// scales it to the window for us.
func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return g.rw, g.rh
}
