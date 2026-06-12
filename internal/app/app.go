// Package app wires the renderer to a window using Ebiten: it owns the game
// loop, translates keyboard/mouse input into camera motion, manages relative
// mouse capture (the analog of the browser's pointer lock), and draws the HUD.
package app

import (
	"fmt"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
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

	// Hot-reload: when a -scene/-player file was given on the command line, its
	// modification time is polled and the scene/config is rebuilt on change so
	// edits show up live. Empty paths disable watching (e.g. the embedded scene).
	scenePath, playerPath string
	sceneMod, playerMod   time.Time
	reloadPoll            int
	reloadMsg             string
	reloadMsgAt           time.Time
}

// New builds a game with the given internal render resolution rendering the
// provided scene, using cfg for player-movement tuning. scenePath/playerPath are
// the files those were loaded from (empty for the built-in defaults); when set,
// they are watched for changes and hot-reloaded.
func New(rw, rh int, sc *scene.Scene, cfg camera.Config, scenePath, playerPath string) *Game {
	g := &Game{
		rw:         rw,
		rh:         rh,
		ren:        render.New(rw, rh),
		cam:        camera.New(),
		tr:         trace.New(sc),
		buf:        make([]byte, rw*rh*4),
		frame:      ebiten.NewImage(rw, rh),
		pixSize:    1,
		scenePath:  scenePath,
		playerPath: playerPath,
	}
	g.tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	g.tr.Prepare() // bake the AO volume now so the first frame doesn't stall
	g.cam.SetConfig(cfg)
	g.cam.SetWorld(sc)
	if sc.Start.Set {
		g.cam.Pos, g.cam.Yaw, g.cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	g.cam.SnapToGround()
	// Seed the watch timestamps so the first poll doesn't trigger a needless
	// reload of the file we just loaded.
	g.sceneMod, _ = fileModTime(scenePath)
	g.playerMod, _ = fileModTime(playerPath)
	return g
}

// Update advances input and camera state once per tick (60 Hz).
func (g *Game) Update() error {
	g.checkReload()
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

// checkReload polls the watched scene/player files (a few times a second) and
// rebuilds on change. A failed parse (e.g. caught mid-save) keeps the current
// scene and is retried on the next poll, so the app never crashes on a bad edit.
func (g *Game) checkReload() {
	if g.scenePath == "" && g.playerPath == "" {
		return
	}
	// Poll ~4x/second rather than every frame; os.Stat is cheap but not free.
	g.reloadPoll++
	if g.reloadPoll < 15 {
		return
	}
	g.reloadPoll = 0

	if g.scenePath != "" {
		if mt, ok := fileModTime(g.scenePath); ok && mt.After(g.sceneMod) {
			if g.reloadScene() {
				g.sceneMod = mt
			}
		}
	}
	if g.playerPath != "" {
		if mt, ok := fileModTime(g.playerPath); ok && mt.After(g.playerMod) {
			if g.reloadPlayer() {
				g.playerMod = mt
			}
		}
	}
}

// reloadScene reloads the scene file and swaps in a fresh tracer, preserving the
// current camera pose and feature toggles. Returns false (keeping the old scene)
// if the file fails to parse.
func (g *Game) reloadScene() bool {
	sc, err := sceneio.Load(g.scenePath)
	if err != nil {
		g.setReloadMsg("scene reload FAILED: " + err.Error())
		return false
	}
	tr := trace.New(sc)
	tr.Opts = g.tr.Opts // keep the user's mirror/shadow/AO toggles
	tr.Time = g.elapsed
	tr.Prepare() // bake the new AO volume before the next frame uses it
	g.tr = tr
	g.cam.SetWorld(sc) // collisions/ground use the new geometry; pose unchanged
	g.setReloadMsg("scene reloaded")
	return true
}

// reloadPlayer reloads the player-movement config. Returns false on parse error.
func (g *Game) reloadPlayer() bool {
	cfg, err := sceneio.LoadPlayer(g.playerPath)
	if err != nil {
		g.setReloadMsg("player reload FAILED: " + err.Error())
		return false
	}
	g.cam.SetConfig(cfg)
	g.setReloadMsg("player config reloaded")
	return true
}

func (g *Game) setReloadMsg(msg string) {
	g.reloadMsg = msg
	g.reloadMsgAt = time.Now()
}

// fileModTime returns the modification time of path, or ok=false if path is
// empty or cannot be stat'd.
func fileModTime(path string) (time.Time, bool) {
	if path == "" {
		return time.Time{}, false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
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

	// Briefly surface the result of a hot-reload.
	if g.reloadMsg != "" && time.Since(g.reloadMsgAt) < 3*time.Second {
		ebitenutil.DebugPrintAt(screen, g.reloadMsg, 4, 18)
	}
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
