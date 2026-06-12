// Package app wires the renderer to a window using Ebiten: it owns the game
// loop, translates keyboard/mouse input into camera motion, manages relative
// mouse capture (the analog of the browser's pointer lock), and draws the HUD.
package app

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"raytracer/internal/audio"
	"raytracer/internal/camera"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/trace"
	"raytracer/internal/vec"
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
	// sceneDeps maps every file the scene depends on (the file itself plus all
	// "extends"/[[include]] targets) to the mod time last seen, so editing an
	// included sub-scene (e.g. building.toml) also triggers a reload.
	scenePath, playerPath string
	sceneDeps             map[string]time.Time
	playerMod             time.Time
	reloadPoll            int
	reloadMsg             string
	reloadMsgAt           time.Time

	// Audio + footsteps. snd is nil when no audio device is available, in which
	// case every audio call is a no-op. strideAccum sums horizontal distance
	// walked while grounded; a footstep fires each time it passes strideLen.
	snd         *audio.Engine
	prevPos     vec.V
	strideAccum float64
	wasGround   bool
	reverbPoll  int
	// Smoothed reverb parameters; updateReverb eases these toward their target
	// each poll so entering/leaving a room fades rather than snapping. wetL/wetR
	// are per-ear so reflections favor the side with nearby walls.
	reverbWetL float64
	reverbWetR float64
	reverbFb   float64
	reverbDamp float64
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
	// reload of the files we just loaded.
	g.sceneDeps = seedSceneDeps(scenePath)
	g.playerMod, _ = fileModTime(playerPath)

	// Audio is best-effort: if no device can be opened (headless/CI), keep snd
	// nil and run silently rather than failing to launch.
	if snd, err := audio.NewEngine(); err == nil {
		g.snd = snd
	}
	g.prevPos = g.cam.Pos
	g.wasGround = g.cam.OnGround()
	g.reverbFb, g.reverbDamp = 0.75, 0.5
	g.setupAmbience()
	return g
}

// Footstep tuning. strideLen is the horizontal distance between steps; the
// pitch of each step is jittered within ±pitchJitter for a natural cadence.
const (
	strideLen   = 1.15
	pitchJitter = 0.12
)

// updateFootsteps accumulates walked distance and fires a footstep sound each
// stride, picking the sound from the surface underfoot and jittering pitch and
// gain per step. It also plays a single, slightly heavier step on landing.
func (g *Game) updateFootsteps() {
	if g.snd == nil {
		return
	}
	pos := g.cam.Pos
	onGround := g.cam.OnGround()

	// Landing thud: airborne → grounded transition.
	if onGround && !g.wasGround {
		g.playStep(pos, 1.0, -0.15)
		g.strideAccum = 0
	} else if onGround {
		dx := pos.X - g.prevPos.X
		dz := pos.Z - g.prevPos.Z
		g.strideAccum += math.Hypot(dx, dz)
		if g.strideAccum >= strideLen {
			g.strideAccum -= strideLen
			g.playStep(pos, 0.0, 0)
		}
	}

	g.prevPos = pos
	g.wasGround = onGround
}

// playStep synthesizes one footstep for the surface under pos. extraGain and
// pitchBias let callers tweak emphasis (e.g. a landing step is louder/lower).
func (g *Game) playStep(pos vec.V, extraGain, pitchBias float64) {
	headY := pos.Y
	mat := surfaceToAudio(g.tr.Scene.StepMaterialAt(pos.X, pos.Z, headY))
	pitch := 1 + pitchBias + (rand.Float64()*2-1)*pitchJitter
	gain := 0.9 + extraGain + (rand.Float64()*2-1)*0.1
	g.snd.Footstep(mat, gain, pitch)
}

// surfaceToAudio maps the scene's step material to the audio engine's surface.
func surfaceToAudio(m scene.StepMaterial) audio.Surface {
	switch m {
	case scene.StepHard:
		return audio.Hard
	case scene.StepWood:
		return audio.Wood
	case scene.StepSnow:
		return audio.Snow
	default:
		return audio.Grass
	}
}

// updateReverb probes the scene around the listener with a few rays and sets the
// reverb to match the enclosure: many nearby wall hits → a wet, longer tail
// (indoors); rays flying off into the open → dry (outdoors). Throttled since the
// acoustics change slowly relative to the frame rate.
func (g *Game) updateReverb() {
	if g.snd == nil {
		return
	}
	g.reverbPoll++
	if g.reverbPoll < 6 {
		return
	}
	g.reverbPoll = 0

	const maxProbe = 25.0
	// Ceiling proximity ramps the reverb in rather than gating it: at/under
	// ceilingNear it's fully "indoors", past ceilingFar it's fully "outdoors",
	// and it blends between. Combined with the temporal easing below, this turns
	// the old hard on/off at a doorway into a smooth fade.
	const ceilingNear, ceilingFar = 4.0, 11.0
	origin := g.cam.Pos

	upDist := g.tr.ProbeDistance(origin, vec.V{Y: 1}, maxProbe)
	ceilFac := clampf((ceilingFar-upDist)/(ceilingFar-ceilingNear), 0, 1)

	// Probe a horizontal fan and bucket each ray's wall hit into the left/right
	// ear by its direction relative to the player's "right" vector. A wall on one
	// side then raises the reverb in that ear only, so walking beside a wall with
	// open space on the other side is heard as one-sided reflections.
	_, right, _ := g.cam.Basis()
	dirs := []vec.V{
		{X: 1}, {X: -1}, {Z: 1}, {Z: -1},
		{X: 0.7, Z: 0.7}, {X: -0.7, Z: 0.7}, {X: 0.7, Z: -0.7}, {X: -0.7, Z: -0.7},
	}
	var hits int
	var sum, lAcc, lW, rAcc, rW float64
	for _, d := range dirs {
		hit := 0.0
		if dist := g.tr.ProbeDistance(origin, d, maxProbe); dist < maxProbe {
			hit = 1
			hits++
			sum += dist
		}
		// Pan weight: +1 fully to the right ear, -1 fully to the left.
		pan := d.Normalize().Dot(right)
		rWeight := clampf((pan+1)/2, 0, 1)
		lWeight := 1 - rWeight
		lAcc += hit * lWeight
		lW += lWeight
		rAcc += hit * rWeight
		rW += rWeight
	}
	leftEncl, rightEncl := 0.0, 0.0
	if lW > 0 {
		leftEncl = lAcc / lW
	}
	if rW > 0 {
		rightEncl = rAcc / rW
	}
	enclosure := float64(hits) / float64(len(dirs)) // overall, for tail character
	avg := maxProbe
	if hits > 0 {
		avg = sum / float64(hits)
	}

	const wetScale = 0.35
	targetWetL := leftEncl * wetScale * ceilFac
	targetWetR := rightEncl * wetScale * ceilFac
	targetFb := 0.72 + clampf(avg/maxProbe, 0, 1)*0.20
	targetDamp := 0.25 + (1-enclosure)*0.35

	// Ease toward the target so crossing a threshold fades over ~0.5 s rather
	// than popping (this poll runs roughly every 0.1 s).
	const k = 0.2
	g.reverbWetL += (targetWetL - g.reverbWetL) * k
	g.reverbWetR += (targetWetR - g.reverbWetR) * k
	g.reverbFb += (targetFb - g.reverbFb) * k
	g.reverbDamp += (targetDamp - g.reverbDamp) * k
	g.snd.SetReverb(g.reverbFb, g.reverbDamp, g.reverbWetL, g.reverbWetR)
}

// setupAmbience (re)builds looping spatial sounds from the scene's [[sound]]
// tables (e.g. crickets in trees). Called on startup and after a scene reload.
func (g *Game) setupAmbience() {
	if g.snd == nil {
		return
	}
	var emitters []audio.AmbientEmitter
	for _, a := range g.tr.Scene.Ambiences {
		emitters = append(emitters, audio.AmbientEmitter{
			Sound:  a.Sound,
			Pos:    a.Pos,
			Gain:   a.Gain,
			Radius: a.Radius,
		})
	}
	g.snd.SetAmbients(emitters)
}

// updateAmbience refreshes distance attenuation, stereo pan, and wall occlusion
// for each ambient emitter from the listener pose.
func (g *Game) updateAmbience() {
	if g.snd == nil {
		return
	}
	_, right, _ := g.cam.Basis()
	g.snd.UpdateAmbients(g.cam.Pos, right, g.ambientOcclusion)
}

// ambientOcclusion ray-traces from the listener toward an ambient emitter. A wall
// hit before the source heavily muffles the sound (cubed falloff), so crickets
// outside are near-silent indoors even when within the distance radius.
func (g *Game) ambientOcclusion(listener, target vec.V) float64 {
	dx := target.X - listener.X
	dy := target.Y - listener.Y
	dz := target.Z - listener.Z
	dist := math.Hypot(dx, math.Hypot(dy, dz))
	if dist < 0.2 {
		return 1
	}
	dir := vec.V{X: dx / dist, Y: dy / dist, Z: dz / dist}
	hit := g.tr.ProbeDistance(listener, dir, dist)
	ratio := hit / dist
	if ratio >= 0.95 {
		return 1 // clear line of sight (or emitter is the nearest surface)
	}
	return ratio * ratio * ratio
}

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// seedSceneDeps resolves every file scenePath depends on and records their
// current mod times. It falls back to watching just the top-level file if the
// dependency walk fails.
func seedSceneDeps(scenePath string) map[string]time.Time {
	if scenePath == "" {
		return nil
	}
	if _, deps, err := sceneio.LoadDeps(scenePath); err == nil && len(deps) > 0 {
		return depTimes(deps)
	}
	return depTimes([]string{scenePath})
}

// depTimes stats each path and returns a path→mod-time map (unreadable files
// are skipped).
func depTimes(paths []string) map[string]time.Time {
	m := make(map[string]time.Time, len(paths))
	for _, p := range paths {
		if mt, ok := fileModTime(p); ok {
			m[p] = mt
		}
	}
	return m
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

	// Footsteps, room reverb, and spatial ambients derive from the post-move state.
	g.updateReverb()
	g.updateAmbience()
	g.updateFootsteps()

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

	if g.scenePath != "" && g.sceneDirty() {
		g.reloadScene() // refreshes the dep timestamps on success
	}
	if g.playerPath != "" {
		if mt, ok := fileModTime(g.playerPath); ok && mt.After(g.playerMod) {
			if g.reloadPlayer() {
				g.playerMod = mt
			}
		}
	}
}

// sceneDirty reports whether any watched scene dependency file changed since it
// was last loaded.
func (g *Game) sceneDirty() bool {
	for p, mt := range g.sceneDeps {
		if cur, ok := fileModTime(p); ok && cur.After(mt) {
			return true
		}
	}
	return false
}

// reloadScene reloads the scene (and all its included files) and swaps in a
// fresh tracer, preserving the current camera pose and feature toggles. Returns
// false (keeping the old scene) if any file fails to parse; the dependency
// timestamps are only refreshed on success, so a bad edit is retried next poll.
func (g *Game) reloadScene() bool {
	sc, deps, err := sceneio.LoadDeps(g.scenePath)
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
	g.sceneDeps = depTimes(deps)
	g.setupAmbience()
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
