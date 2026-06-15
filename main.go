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
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/webgpu"
)

const (
	renderW = 512 // internal render resolution (matches the original)
	renderH = renderW * 0.625
	scale   = 3 // window is scale x the internal resolution
)

//go:embed scenes/default.toml
var defaultSceneTOML []byte

//go:embed player.toml
var defaultPlayerTOML []byte

func main() {
	scenePath := flag.String("scene", "", "path to a TOML scene file (default: built-in scene)")
	playerPath := flag.String("player", "", "path to a TOML player-movement config (default: built-in)")
	renderer := flag.String("renderer", "cpu", "renderer backend: cpu|webgpu")
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

	var ren render.Renderer = render.New(renderW, renderH)
	if *renderer == "webgpu" {
		gpu, err := webgpu.New(renderW, renderH)
		if err != nil {
			log.Printf("webgpu unavailable, falling back to cpu: %v", err)
		} else {
			ren = gpu
		}
	} else if *renderer != "cpu" {
		log.Fatalf("unknown renderer %q (want cpu|webgpu)", *renderer)
	}

	game := app.New(renderW, renderH, sc, cfg, *scenePath, *playerPath)
	game.SetRenderer(ren)

	ebiten.SetWindowSize(renderW*scale, renderH*scale)
	ebiten.SetWindowTitle("Realtime Raytracer (Go + Ebiten)")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
