// Package app wires the renderer to a window using Ebiten: it owns the game
// loop, translates keyboard/mouse input into camera motion, manages relative
// mouse capture (the analog of the browser's pointer lock), and draws the HUD.
package app

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"golang.org/x/image/font"

	"raytracer/internal/audio"
	"raytracer/internal/camera"
	"raytracer/internal/document"
	"raytracer/internal/door"
	"raytracer/internal/interactlight"
	"raytracer/internal/joltphys"
	"raytracer/internal/npc"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/scenestate"
	"raytracer/internal/screen"
	"raytracer/internal/vec"
)

// Game is the Ebiten game implementing the render loop and input handling.
type Game struct {
	rw, rh int

	ren render.Renderer
	cam *camera.Camera

	// sc is the active scene; pb answers acoustic ray queries against it (room
	// reverb and ambient-source occlusion). aoData/aoOK is the baked
	// ambient-occlusion volume handed to the renderer each frame.
	sc     *scene.Scene
	pb     *probe.Probe
	aoData probe.AOData
	aoOK   bool
	aoMu   sync.Mutex
	aoVer  uint64
	aoBake uint64

	// basePlayerCfg is the global player.toml tuning; scene [player.movement]
	// overrides are merged on top when the scene is (re)loaded.
	basePlayerCfg camera.Config
	shadow        bool
	mirror        bool
	thinGlassGhost bool
	ao            bool
	adaptiveAA    bool
	// colorQuant: 0 = 8-bit dither, 1 = 15-bit (default), 2 = crush (24 levels/ch). Key 5 cycles.
	colorQuant uint32

	buf   []byte
	frame *ebiten.Image

	pixSize int
	locked  bool
	prevCX  int
	prevCY  int
	elapsed float64 // animation clock in seconds

	// padIDs is a reused scratch buffer for polling connected gamepads.
	padIDs []ebiten.GamepadID

	// hudHidden hides the dev overlay (fps/status/timings/help) when true; the
	// transient hot-reload toast still shows so scene edits are confirmed even
	// on a clean view. Toggled with the "0" key.
	hudHidden bool

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

	// Interaction hint shown when near an interactable object.
	activeHint    string
	hintFont      font.Face
	hintCached    string
	hintCachedImg *ebiten.Image
	hintCachedW   int
	hintCachedH   int
	// Screen fade during portal transitions (0 = clear, 1 = black).
	fadeAlpha        float64
	fadeTarget       float64
	transitionActive bool
	portalPhase      portalPhase

	hudSmooth hudSmoother

	// hudPos is the throttled world-position string shown on the dev overlay.
	hudPos   string
	hudPosAt time.Time

	// fpsCap throttles how often a new frame is ray-traced (0 = uncapped). The
	// H key cycles uncapped -> 60 -> 30. Between traces the last frame is
	// re-presented, so the GPU idles instead of tracing back-to-back (cooler
	// laptop) without changing what any frame looks like. nextTrace is the
	// drift-free deadline for the next trace; traceFPS is the measured trace
	// rate shown on the HUD.
	fpsCap      int
	nextTrace   time.Time
	lastTraceAt time.Time
	traceFPS    float64

	npcs *npc.Manager

	doors *door.Manager

	documents *document.Manager

	screens *screen.Manager

	interactLights *interactlight.Manager
	state          *scenestate.Manager

	// npcDebug draws skeleton/foot overlay segments (key 6).
	npcDebug bool

	spyglass Spyglass
	torch    Torch

	jolt *joltphys.World
}

// New builds a game with the given internal render resolution rendering the
// provided scene through ren. basePlayerCfg is the global player-movement tuning
// (from player.toml); per-scene [player.movement] overrides are merged on top.
// scenePath/playerPath are the files those were loaded from (empty for the
// built-in defaults); when set, they are watched for changes and hot-reloaded.
func New(rw, rh int, sc *scene.Scene, basePlayerCfg camera.Config, scenePath, playerPath string, ren render.Renderer) *Game {
	g := &Game{
		rw:            rw,
		rh:            rh,
		ren:           ren,
		cam:           camera.New(),
		basePlayerCfg: basePlayerCfg,
		shadow:        true,
		mirror:         true,
		thinGlassGhost: true,
		ao:             true,
		adaptiveAA:    true,
		colorQuant:    1,
		buf:           make([]byte, rw*rh*4),
		frame:         ebiten.NewImage(rw, rh),
		pixSize:       1,
		fpsCap:        60, // default cap: cuts GPU power vs uncapped, still smooth
		scenePath:     scenePath,
		playerPath:    playerPath,
	}
	g.setScene(sc) // builds the probe, bakes AO, binds the camera's world
	g.applyPlayerConfig()
	if sc.Start.Set {
		g.cam.Pos, g.cam.Yaw, g.cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
		g.cam.Land()
	} else {
		g.cam.SnapToGround()
	}
	g.syncJoltPlayer()
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

// setScene binds sc as the active world: it builds the acoustic probe, starts
// an ambient-occlusion bake in the background (so the window can appear sooner),
// and points the camera's collision world at the new geometry. The camera pose
// is left untouched so a hot-reload keeps the player in place.
func (g *Game) setScene(sc *scene.Scene) {
	g.sc = sc
	if sync, ok := g.ren.(render.DocumentTexturesSyncer); ok {
		sync.SyncDocumentTextures()
	} else if inv, ok := g.ren.(render.DocumentTexturesInvalidator); ok {
		inv.InvalidateDocumentTextures()
	}
	g.pb = probe.New(sc)
	g.npcs = npc.NewManager()
	_ = g.npcs.Instantiate(sc, npc.FootWorld(sc))
	g.doors = door.NewManager()
	if err := g.doors.Instantiate(sc); err != nil {
		fmt.Fprintf(os.Stderr, "doors: %v\n", err)
	}
	g.wireDoorSounds()
	g.documents = document.NewManager()
	if err := g.documents.Instantiate(sc); err != nil {
		fmt.Fprintf(os.Stderr, "documents: %v\n", err)
	}
	g.screens = screen.NewManager()
	if err := g.screens.Instantiate(sc); err != nil {
		fmt.Fprintf(os.Stderr, "screens: %v\n", err)
	}
	stateMgr, err := scenestate.NewManager(sc.Reactive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "state: %v\n", err)
	}
	g.state = stateMgr
	if g.state != nil {
		if err := g.state.Instantiate(sc); err != nil {
			fmt.Fprintf(os.Stderr, "state instantiate: %v\n", err)
		}
	}
	g.interactLights = interactlight.NewManager()
	skipStateLight := func(i int) bool { return g.state != nil && g.state.IsStateLight(i) }
	g.interactLights.Instantiate(sc, skipStateLight)
	sc.SetDoorGhost(func(i int) bool {
		if g.doors != nil && g.doors.GhostBox(i) {
			return true
		}
		if g.documents != nil && g.documents.GhostBox(i) {
			return true
		}
		return false
	})
	if err := g.spyglass.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "spyglass: %v\n", err)
	}
	if err := g.torch.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "torch: %v\n", err)
	}
	g.aoMu.Lock()
	g.aoOK = false
	g.aoData = probe.AOData{}
	g.aoVer++
	bakeGen := g.aoVer
	g.aoBake = bakeGen
	g.aoMu.Unlock()
	go g.bakeAO(bakeGen)
}

func (g *Game) bakeAO(gen uint64) {
	data, ok := g.pb.BakeAO()
	g.aoMu.Lock()
	defer g.aoMu.Unlock()
	if gen != g.aoBake {
		return
	}
	g.aoData = data
	g.aoOK = ok
	g.aoVer++
}

// view assembles the per-frame render state from the current scene, clock and
// feature toggles.
func (g *Game) view() *render.View {
	g.aoMu.Lock()
	aoData, aoOK, aoVer := g.aoData, g.aoOK, g.aoVer
	g.aoMu.Unlock()
	return &render.View{
		Scene:          g.sc,
		Time:           g.elapsed,
		Shadow:         g.shadow,
		Mirror:         g.mirror,
		ThinGlassGhost: g.thinGlassGhost,
		AO:             g.ao,
		AOData:         aoData,
		AOok:           aoOK,
		AOVersion:      aoVer,
		ColorQuant:     g.colorQuant,
		AdaptiveAA:     g.adaptiveAA,
		MaxBounceDepth: 4,
	}
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
		g.playStep(pos, 0.4, -0.15)
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
	mat := surfaceToAudio(g.sc.StepMaterialAt(pos.X, pos.Z, headY))
	pitch := 1 + pitchBias + (rand.Float64()*2-1)*pitchJitter
	gain := 0.1 + extraGain + (rand.Float64()*2-1)*0.05
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

	upDist := g.pb.Distance(origin, vec.V{Y: 1}, maxProbe)
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
		if dist := g.pb.Distance(origin, d, maxProbe); dist < maxProbe {
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
	for _, a := range g.sc.Ambiences {
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
	hit := g.pb.Distance(listener, dir, dist)
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
	g.handleNPCDebugKeys()

	// Mouse look while captured (relative motion, like pointer lock).
	if g.locked {
		cx, cy := ebiten.CursorPosition()
		g.cam.Look(float64(cx-g.prevCX), float64(cy-g.prevCY))
		g.prevCX, g.prevCY = cx, cy
	}

	if !g.transitionActive {
		// Fixed-step dt matching the original (clamped to 0.1 of a 60 Hz frame).
		const dt = 0.1
		mv := camera.Move{}
		readingDoc := g.documents != nil && g.documents.Reading()
		viewingScreen := g.screens != nil && g.screens.Viewing()
		if !readingDoc && !viewingScreen {
			mv = camera.Move{
				Forward: ebiten.IsKeyPressed(ebiten.KeyW) || ebiten.IsKeyPressed(ebiten.KeyArrowUp),
				Back:    ebiten.IsKeyPressed(ebiten.KeyS) || ebiten.IsKeyPressed(ebiten.KeyArrowDown),
				Left:    ebiten.IsKeyPressed(ebiten.KeyA) || ebiten.IsKeyPressed(ebiten.KeyArrowLeft),
				Right:   ebiten.IsKeyPressed(ebiten.KeyD) || ebiten.IsKeyPressed(ebiten.KeyArrowRight),
				Jump:    ebiten.IsKeyPressed(ebiten.KeySpace),
				Sprint:  ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight),
				Crouch:  ebiten.IsKeyPressed(ebiten.KeyC),
			}
		}
		if !readingDoc && !viewingScreen {
			g.applyGamepad(&mv)
		} else if readingDoc || viewingScreen {
			g.applyGamepadLook()
		}
		if !readingDoc && !viewingScreen {
			if g.jolt != nil {
				g.jolt.UpdatePlayer(g.cam, mv, dt)
			} else {
				g.cam.Update(mv, dt)
			}
		}
		g.updateHUDPos()

		if g.pb != nil {
			g.pb.Sync(g.sc)
		}

		// Footsteps, room reverb, and spatial ambients derive from the post-move state.
		g.updateReverb()
		g.updateAmbience()
		g.updateFootsteps()

		g.handleInteract()

		if g.npcs != nil {
			const npcDt = 1.0 / 60.0
			g.npcs.Update(g.sc, npc.FootWorld(g.sc), npcDt)
		}
		if g.doors != nil {
			feetY := g.cam.Pos.Y - g.cam.EyeHeight()
			headY := g.cam.Pos.Y + 0.15
			g.doors.Update(g.sc, g.cam.Pos, feetY, headY, 1.0/60.0)
		}
		if g.documents != nil {
			g.documents.Update(g.sc, g.cam, 1.0/60.0)
		}
		if g.screens != nil {
			aspect := float64(g.rw) / float64(g.rh)
			g.screens.Update(g.sc, g.cam, aspect, 1.0/60.0)
		}
		if g.interactLights != nil {
			g.interactLights.Update(g.sc, 1.0/60.0)
		}
		g.spyglass.Update(g.sc, g.cam, 1.0/60.0)
		g.torch.Update(g.sc, g.cam, 1.0/60.0)
	}
	g.updateFade()
	g.updatePortalTransition()

	// Advance the animation clock (Update runs at a fixed 60 Hz). view() reads
	// it each frame, so there is nothing else to push.
	g.elapsed += 1.0 / 60.0

	return nil
}

// Gamepad tuning. Sticks need a deadzone to ignore resting drift; triggers rest
// at 0 so they only need a small one. padLookSpeed is the look delta (in the
// same "pixels" units the mouse uses) applied at full stick deflection.
const (
	padStickDeadzone   = 0.15
	padTriggerDeadzone = 0.06
	padLookSpeed       = 13.0
)

// activeGamepad returns the first connected gamepad exposing the SDL standard
// layout (so the stick/trigger/button mapping below is consistent across pads).
func (g *Game) activeGamepad() (ebiten.GamepadID, bool) {
	g.padIDs = ebiten.AppendGamepadIDs(g.padIDs[:0])
	for _, id := range g.padIDs {
		if ebiten.IsStandardGamepadLayoutAvailable(id) {
			return id, true
		}
	}
	return 0, false
}

// applyGamepad layers analog controller input onto mv: left stick walks, right
// stick looks (both analog), the left/right triggers crouch/run, the bottom
// face button (A/cross) jumps, and the left face button (X/square) uses.
func (g *Game) applyGamepad(mv *camera.Move) {
	id, ok := g.activeGamepad()
	if !ok {
		return
	}

	lx, ly := stickDeadzone(
		ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickHorizontal),
		ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickVertical),
	)
	g.applyGamepadLook()

	// Left stick walks: right strafes, up (negative axis) is forward.
	mv.MoveX += lx
	mv.MoveZ += -ly

	mv.CrouchAxis = triggerValue(ebiten.StandardGamepadButtonValue(id, ebiten.StandardGamepadButtonFrontBottomLeft))
	mv.SprintAxis = triggerValue(ebiten.StandardGamepadButtonValue(id, ebiten.StandardGamepadButtonFrontBottomRight))
	if ebiten.IsStandardGamepadButtonPressed(id, ebiten.StandardGamepadButtonRightBottom) {
		mv.Jump = true
	}
}

func (g *Game) applyGamepadLook() {
	id, ok := g.activeGamepad()
	if !ok {
		return
	}
	rx, ry := stickDeadzone(
		ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisRightStickHorizontal),
		ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisRightStickVertical),
	)
	g.cam.Look(expo(rx)*padLookSpeed, expo(ry)*padLookSpeed)
}

// stickDeadzone applies a radial deadzone and rescales the remaining range to
// 0..1 so motion begins smoothly just past the deadzone rather than jumping.
func stickDeadzone(x, y float64) (float64, float64) {
	m := math.Hypot(x, y)
	if m <= padStickDeadzone {
		return 0, 0
	}
	s := (m - padStickDeadzone) / (1 - padStickDeadzone)
	if s > 1 {
		s = 1
	}
	s /= m
	return x * s, y * s
}

// triggerValue clamps a trigger's small resting value to 0 and rescales the rest
// to 0..1.
func triggerValue(v float64) float64 {
	if v <= padTriggerDeadzone {
		return 0
	}
	return (v - padTriggerDeadzone) / (1 - padTriggerDeadzone)
}

// expo applies a square response curve (preserving sign) for finer aiming near
// the stick's center while keeping full speed at full deflection.
func expo(v float64) float64 { return v * math.Abs(v) }

// checkReload polls the watched scene/player files (a few times a second) and
// rebuilds on change. A failed parse (e.g. caught mid-save) keeps the current
// scene and is retried on the next poll, so the app never crashes on a bad edit.
func (g *Game) checkReload() {
	if g.scenePath == "" && g.playerPath == "" && g.torch.srcPath == "" {
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
	if reloaded, err := g.torch.ReloadIfDirty(g.sc); err != nil {
		g.setReloadMsg("torch reload FAILED: " + err.Error())
	} else if reloaded {
		g.setReloadMsg("torch reloaded")
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

// reloadScene reloads the scene (and all its included files) and rebinds it,
// preserving the current camera pose and feature toggles. Returns false (keeping
// the old scene) if any file fails to parse; the dependency timestamps are only
// refreshed on success, so a bad edit is retried next poll.
func (g *Game) reloadScene() bool {
	sc, deps, err := sceneio.LoadDeps(g.scenePath)
	if err != nil {
		g.setReloadMsg("scene reload FAILED: " + err.Error())
		return false
	}
	var doorSnap []door.AgentSnap
	if g.doors != nil && g.sc != nil {
		doorSnap = g.doors.Snapshot(g.sc)
	}
	g.setScene(sc) // rebuilds the probe, re-bakes AO; pose/toggles unchanged
	g.applyPlayerConfig()
	if len(doorSnap) > 0 {
		g.doors.Restore(sc, doorSnap)
	}
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
	g.basePlayerCfg = cfg
	g.applyPlayerConfig()
	g.setReloadMsg("player config reloaded")
	return true
}

func (g *Game) applyPlayerConfig() {
	if g.sc == nil {
		g.cam.SetConfig(g.basePlayerCfg)
		g.bindPhysicsWorld()
		return
	}
	g.cam.SetConfig(camera.MergeConfig(g.basePlayerCfg, g.sc.PlayerMovement))
	g.bindPhysicsWorld()
}

func (g *Game) bindPhysicsWorld() {
	if g.jolt != nil {
		g.jolt.Destroy()
		g.jolt = nil
	}
	if g.sc == nil {
		return
	}
	cfg := g.cam.Config()
	if !cfg.JoltPhysics {
		g.cam.SetWorld(g.sc)
		return
	}
	w, err := joltphys.NewWorldFromScene(g.sc, g.cam.Pos, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jolt physics: %v (using CPU collision)\n", err)
		g.cam.SetWorld(g.sc)
		return
	}
	g.jolt = w
	g.cam.SetWorld(w)
}

func (g *Game) syncJoltPlayer() {
	if g.jolt != nil {
		g.jolt.SyncPlayer(g.cam.Pos)
	}
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
		g.mirror = !g.mirror
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit2) {
		g.shadow = !g.shadow
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit3) {
		g.ao = !g.ao
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit4) {
		g.cam.NoClip = !g.cam.NoClip
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit5) {
		g.colorQuant = (g.colorQuant + 1) % 3
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit7) {
		g.adaptiveAA = !g.adaptiveAA
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit8) {
		g.thinGlassGhost = !g.thinGlassGhost
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit0) {
		g.hudHidden = !g.hudHidden
		if g.hudHidden {
			g.hudSmooth.reset()
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyQ) {
		g.spyglass.Toggle()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF) {
		g.torch.Toggle()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyH) {
		switch g.fpsCap {
		case 0:
			g.fpsCap = 60
		case 60:
			g.fpsCap = 30
		default:
			g.fpsCap = 0
		}
		g.nextTrace = time.Now() // re-arm the schedule so the change takes effect now
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

// shouldTrace reports whether this Draw should ray-trace a new frame, honoring
// the H-key FPS cap. It uses a fixed-step deadline (advanced by the cap
// interval, not reset to now) so the average trace rate stays exact without
// drift, and records the measured trace rate for the HUD.
func (g *Game) shouldTrace() bool {
	now := time.Now()
	if g.fpsCap <= 0 {
		g.recordTrace(now)
		return true
	}
	if g.nextTrace.IsZero() || !now.Before(g.nextTrace) {
		interval := time.Second / time.Duration(g.fpsCap)
		g.nextTrace = g.nextTrace.Add(interval)
		if g.nextTrace.Before(now) {
			// Fell behind (e.g. a slow frame or a just-changed cap); re-anchor.
			g.nextTrace = now.Add(interval)
		}
		g.recordTrace(now)
		return true
	}
	return false
}

// recordTrace updates the smoothed trace-rate estimate from the gap between
// successive traced frames.
func (g *Game) recordTrace(now time.Time) {
	if !g.lastTraceAt.IsZero() {
		if dt := now.Sub(g.lastTraceAt).Seconds(); dt > 0 {
			g.traceFPS = 1 / dt
		}
	}
	g.lastTraceAt = now
}

// Draw renders the scene into the framebuffer and blits it with the HUD.
func (g *Game) Draw(screen *ebiten.Image) {
	if wl, ok := g.ren.(render.LiveWorkloadController); ok {
		wl.SetLiveWorkload(!g.hudHidden)
	}
	// Trace a new frame only when uncapped or the cap's deadline has arrived;
	// otherwise re-present the last traced frame so the GPU rests between traces.
	if g.shouldTrace() {
		g.ren.Render(g.buf, g.cam, g.view(), g.pixSize)
		g.frame.WritePixels(g.buf)
	}
	screen.DrawImage(g.frame, nil)

	// "0" hides the dev overlay for a clean view; the reload toast below still
	// shows so hot-reloads are confirmed while iterating on a scene.
	y := 4
	if !g.hudHidden {
		var gpuMS float64
		if prof, ok := g.ren.(render.PhaseTimingsProvider); ok {
			gpuMS = prof.LastPhaseTimings().GPU
		}
		smoothGPU, smoothFPS := g.hudSmooth.sample(gpuMS, g.traceFPS)
		hud := fmt.Sprintf("%s | %s",
			g.frameBudgetLine(smoothGPU, smoothFPS), g.statusLine())
		ebitenutil.DebugPrintAt(screen, hud, 4, y)
		y += 14
		if g.hudPos != "" {
			ebitenutil.DebugPrintAt(screen, g.hudPos, 4, y)
			y += 14
		}

		if wl, ok := g.ren.(render.GPUWorkloadProvider); ok {
			if w := wl.LastGPUWorkload(); w.Ready {
				line1, line2 := g.workloadHUD(w)
				ebitenutil.DebugPrintAt(screen, line1, 4, y)
				y += 14
				ebitenutil.DebugPrintAt(screen, line2, 4, y)
				y += 14
			}
		}
		ebitenutil.DebugPrintAt(screen, g.helpLine(), 4, g.rh-14)
	}

	g.drawInteractHint(screen)
	g.drawNPCDebug(screen)
	g.drawFade(screen)

	// Briefly surface the result of a hot-reload (kept even when the HUD is off).
	if g.reloadMsg != "" && time.Since(g.reloadMsgAt) < 3*time.Second {
		ebitenutil.DebugPrintAt(screen, g.reloadMsg, 4, y)
	}
}

func (g *Game) frameBudgetLine(gpuMS, fps float64) string {
	return render.FormatFrameBudget(gpuMS, fps, g.fpsCap)
}

func (g *Game) workloadHUD(w render.GPUWorkload) (string, string) {
	return render.FormatWorkloadHUD(w)
}

// backendName reports the active renderer's backend label for the HUD.
func (g *Game) backendName() string {
	if b, ok := g.ren.(render.BackendNamer); ok {
		return b.BackendName()
	}
	return "gpu"
}

func (g *Game) statusLine() string {
	if g.locked {
		return fmt.Sprintf("mirror[1]:%s shadow[2]:%s AO[3]:%s noclip[4]:%s color[5]:%s npc[6]:%s AA[7]:%s ghost[8]:%s px[-/+]:%d fps[H]:%s  HUD[0]  ESC release",
			onOff(g.mirror), onOff(g.shadow), onOff(g.ao), onOff(g.cam.NoClip), quantLabel(g.colorQuant), onOff(g.npcDebug), onOff(g.adaptiveAA), onOff(g.thinGlassGhost), g.pixSize, capLabel(g.fpsCap))
	}
	return "click to capture mouse"
}

func (g *Game) helpLine() string {
	return "WASD/arrows move   mouse look   Space jump   Shift sprint   C crouch   E/X use   F torch   Q spyglass   6 NPC debug   P pose dump+report   pad: sticks move/look, triggers crouch/run, A jump, X use"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func quantLabel(q uint32) string {
	switch q {
	case 0:
		return "8bit"
	case 1:
		return "15bit"
	case 2:
		return "crush"
	default:
		return "?"
	}
}

func capLabel(cap int) string {
	if cap <= 0 {
		return "off"
	}
	return fmt.Sprintf("%d", cap)
}

// Layout fixes the logical screen to the internal render resolution; Ebiten
// scales it to the window for us.
func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return g.rw, g.rh
}
