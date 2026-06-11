// Command raytracer is a realtime CPU raytracer rendered with Ebiten. It is a
// modular Go port of the original single-file realtime_raytracer_dos_geo.html,
// preserving the WASD + mouse-look FPS controls (click to capture the mouse,
// ESC to release) and jump.
//
// The scene is described in TOML. A copy of the default scene is embedded in
// the binary; pass -scene <file> to load and iterate on an external scene.
package main

import (
	_ "embed"
	"flag"
	"log"

	"github.com/hajimehoshi/ebiten/v2"

	"raytracer/internal/app"
	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
)

const (
	renderW = 400 // internal render resolution (matches the original)
	renderH = 250
	scale   = 3 // window is scale x the internal resolution
)

//go:embed scenes/default.toml
var defaultSceneTOML []byte

//go:embed player.toml
var defaultPlayerTOML []byte

func main() {
	scenePath := flag.String("scene", "", "path to a TOML scene file (default: built-in scene)")
	playerPath := flag.String("player", "", "path to a TOML player-movement config (default: built-in)")
	flag.Parse()

	var (
		sc  *scene.Scene
		err error
	)
	if *scenePath != "" {
		sc, err = sceneio.Load(*scenePath)
	} else {
		sc, err = sceneio.Decode(defaultSceneTOML)
	}
	if err != nil {
		log.Fatal(err)
	}

	var cfg camera.Config
	if *playerPath != "" {
		cfg, err = sceneio.LoadPlayer(*playerPath)
	} else {
		cfg, err = sceneio.DecodePlayer(defaultPlayerTOML)
	}
	if err != nil {
		log.Fatal(err)
	}

	game := app.New(renderW, renderH, sc, cfg)

	ebiten.SetWindowSize(renderW*scale, renderH*scale)
	ebiten.SetWindowTitle("Realtime Raytracer (Go + Ebiten)")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
