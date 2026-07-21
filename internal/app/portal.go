package app

import (
	"fmt"
	"image/color"
	"log"
	"path/filepath"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/texture"
)

const exitPortalScene = "office-sunset/index.toml"
const exitPortalSpawnID = "cube_lab_1"

type portalPhase int

const (
	portalIdle portalPhase = iota
	portalFadeIn
	portalFadeOut
)

func handleExitPortal(ctx *UseContext) error {
	g := ctx.Game
	if g == nil {
		return fmt.Errorf("no game")
	}
	if g.portalPhase != portalIdle {
		return nil
	}
	g.beginPortalTransition()
	return nil
}

func (g *Game) beginPortalTransition() {
	g.transitionActive = true
	g.portalPhase = portalFadeIn
	g.fadeTarget = 1
}

func (g *Game) updatePortalTransition() {
	switch g.portalPhase {
	case portalFadeIn:
		if g.fadeAlpha < 0.99 {
			return
		}
		if err := g.completePortalTransition(); err != nil {
			log.Printf("exit portal: %v", err)
			g.cancelPortalTransition()
		}
	case portalFadeOut:
		if g.fadeAlpha <= 0.01 {
			g.portalPhase = portalIdle
			g.transitionActive = false
		}
	}
}

func (g *Game) completePortalTransition() error {
	cap := g.capturePortalViews()
	texture.CommitCaptures(cap)

	scenePath := resolveScenePath(g.scenePath, exitPortalScene)
	sc, depsList, err := sceneio.LoadDeps(scenePath)
	if err != nil {
		return fmt.Errorf("load portal scene: %w", err)
	}
	g.loadPortalScene(sc, depTimes(depsList), scenePath)
	ApplyCapturesToScene(sc)
	if err := spawnPlayerAt(g.cam, sc, exitPortalSpawnID); err != nil {
		return err
	}

	g.portalPhase = portalFadeOut
	g.fadeTarget = 0
	return nil
}

func (g *Game) cancelPortalTransition() {
	g.portalPhase = portalIdle
	g.transitionActive = false
	g.fadeTarget = 0
}

func (g *Game) loadPortalScene(sc *scene.Scene, deps map[string]time.Time, path string) {
	g.setScene(sc)
	g.applyPlayerConfig()
	g.scenePath = path
	g.sceneDeps = deps
	g.setupAmbience()
}

func resolveScenePath(current, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	if current != "" {
		return filepath.Join(filepath.Dir(current), rel)
	}
	return filepath.Join("scenes", rel)
}

func (g *Game) updateFade() {
	const speed = 0.12
	if g.fadeAlpha < g.fadeTarget {
		g.fadeAlpha += speed
		if g.fadeAlpha > g.fadeTarget {
			g.fadeAlpha = g.fadeTarget
		}
	} else if g.fadeAlpha > g.fadeTarget {
		g.fadeAlpha -= speed
		if g.fadeAlpha < g.fadeTarget {
			g.fadeAlpha = g.fadeTarget
		}
	}
}

func (g *Game) drawFade(screen *ebiten.Image) {
	if g.fadeAlpha <= 0.01 {
		return
	}
	sw, sh := float32(screen.Bounds().Dx()), float32(screen.Bounds().Dy())
	alpha := uint8(g.fadeAlpha * 255)
	vector.DrawFilledRect(screen, 0, 0, sw, sh, color.RGBA{0, 0, 0, alpha}, false)
}
