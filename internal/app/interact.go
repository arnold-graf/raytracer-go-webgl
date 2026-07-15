package app

import (
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
)

func (g *Game) pickInteractable() *scene.Interactable {
	if g.sc == nil {
		return nil
	}
	fwd, right, up := g.cam.Basis()
	aspect := float64(g.rw) / float64(g.rh)
	ray := g.cam.Ray(fwd, right, up, 0, 0, aspect, camera.FOVScale)
	return g.sc.PickInteractable(ray)
}

func (g *Game) handleInteract() {
	if g.sc == nil || g.transitionActive {
		return
	}
	if g.documents != nil && g.documents.Reading() {
		g.activeHint = "put away"
		if g.useJustPressed() {
			g.documents.Dismiss(g.sc)
		}
		return
	}
	if g.screens != nil && g.screens.Viewing() {
		g.activeHint = "step back"
		if g.useJustPressed() {
			g.screens.Dismiss(g.sc, g.cam)
		}
		return
	}
	ia := g.pickInteractable()
	g.activeHint = ""
	if ia != nil && g.canUseInteract(ia) {
		g.activeHint = ia.Hint
	}
	if ia == nil || !g.canUseInteract(ia) || !g.useJustPressed() {
		return
	}
	h := UseHandlers[ia.Handler]
	if h == nil {
		log.Printf("unknown interact handler %q", ia.Handler)
		return
	}
	ctx := &UseContext{
		Game:     g,
		Camera:   g.cam,
		Renderer: g.ren,
		Interact: ia,
	}
	if err := h(ctx); err != nil {
		log.Printf("interact handler %q: %v", ia.Handler, err)
	}
}

func (g *Game) canUseInteract(ia *scene.Interactable) bool {
	if ia == nil {
		return false
	}
	if ia.Handler == "door" && g.doors != nil {
		return g.doors.CanUseInteract(ia, g.cam.Pos)
	}
	return true
}

// useJustPressed reports E or the left face button (X/square), left of jump (A/cross).
func (g *Game) useJustPressed() bool {
	if inpututil.IsKeyJustPressed(ebiten.KeyE) {
		return true
	}
	id, ok := g.activeGamepad()
	if !ok {
		return false
	}
	return inpututil.IsStandardGamepadButtonJustPressed(id, ebiten.StandardGamepadButtonRightLeft)
}
