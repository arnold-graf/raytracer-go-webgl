package app

import (
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

func (g *Game) handleInteract() {
	if g.sc == nil || g.transitionActive {
		return
	}
	if g.documents != nil && g.documents.Reading() {
		g.activeHint = "press E to put away"
		if inpututil.IsKeyJustPressed(ebiten.KeyE) {
			g.documents.Dismiss(g.sc)
		}
		return
	}
	ia := g.sc.NearestInteractable(g.cam.Pos)
	g.activeHint = ""
	if ia != nil {
		g.activeHint = ia.Hint
	}
	if ia == nil || !inpututil.IsKeyJustPressed(ebiten.KeyE) {
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

func (g *Game) drawInteractHint(screen *ebiten.Image) {
	if g.activeHint == "" || g.transitionActive {
		return
	}
	sw, sh := screen.Bounds().Dx(), screen.Bounds().Dy()
	margin := 24
	x := (sw - len(g.activeHint)*8) / 2
	if x < margin {
		x = margin
	}
	y := sh - margin - 14
	ebitenutil.DebugPrintAt(screen, g.activeHint, x, y)
}
