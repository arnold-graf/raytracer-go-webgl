// Command raytracer is a realtime WebGPU path tracer rendered with Ebiten. It is
// a modular Go port of the original single-file realtime_raytracer_dos_geo.html,
// preserving the WASD + mouse-look FPS controls (click to capture the mouse,
// ESC to release) and jump.
//
// The scene is described in TOML. A copy of the default scene is embedded in
// the binary; pass -scene <file> to load and iterate on an external scene.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"

	"github.com/hajimehoshi/ebiten/v2"

	"raytracer/internal/app"
	"raytracer/internal/camera"
	"raytracer/internal/character"
	"raytracer/internal/joltphys"
	"raytracer/internal/npc"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/webgpu"
)

const (
	renderW = 512 // internal render resolution
	renderH = renderW * 0.625
	scale   = 2 // window is scale x the internal resolution
)

//go:embed scenes/default.toml
var defaultSceneTOML []byte

//go:embed player.toml
var defaultPlayerTOML []byte

func main() {
	scenePath := flag.String("scene", "", "path to a TOML scene file (default: built-in scene)")
	playerPath := flag.String("player", "", "path to a TOML player-movement config (default: built-in)")
	dumpPoses := flag.String("dump-npc-poses", "", "write JSONL NPC pose dump to path and exit")
	dumpFrames := flag.Int("dump-npc-frames", 120, "frames for -dump-npc-poses")
	dumpReport := flag.String("dump-npc-report", "", "write gait analysis report (default: <poses>.report.txt)")
	analyzePoses := flag.String("analyze-npc-poses", "", "analyze an existing JSONL pose dump and print report")
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

	if *analyzePoses != "" {
		recs, err := character.ReadPoseRecords(*analyzePoses)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(character.FormatGaitReport(character.AnalyzePoseRecords(recs)))
		fmt.Println("\nPer-frame summary (last 20):")
		start := 0
		if len(recs) > 20 {
			start = len(recs) - 20
		}
		for _, rec := range recs[start:] {
			fmt.Println(character.FormatFrameSummary(rec))
		}
		return
	}

	if *dumpPoses != "" {
		report := *dumpReport
		if report == "" {
			report = *dumpPoses + ".report.txt"
		}
		if err := npc.DumpPosesWithReport(sc, npc.FootWorld(sc), *dumpFrames, *dumpPoses, report); err != nil {
			log.Fatal(err)
		}
		recs, err := character.ReadPoseRecords(*dumpPoses)
		if err == nil {
			fmt.Print(character.FormatGaitReport(character.AnalyzePoseRecords(recs)))
		}
		return
	}

	var basePlayerCfg camera.Config
	if *playerPath != "" {
		basePlayerCfg, err = sceneio.LoadPlayer(*playerPath)
	} else {
		basePlayerCfg, err = sceneio.DecodePlayer(defaultPlayerTOML)
	}
	if err != nil {
		log.Fatal(err)
	}

	ren, err := webgpu.New(renderW, renderH)
	if err != nil {
		log.Fatalf("webgpu renderer unavailable: %v", err)
	}
	// Pipeline the interactive loop: submit each frame and hand back the previous
	// one so the GPU renders while the CPU packs/blits, instead of stalling on it.
	ren.SetPipelined(true)

	if err := joltphys.Init(); err != nil {
		log.Fatalf("jolt physics: %v", err)
	}
	defer joltphys.Shutdown()

	game := app.New(renderW, renderH, sc, basePlayerCfg, *scenePath, *playerPath, ren)

	ebiten.SetWindowSize(renderW*scale, renderH*scale)
	ebiten.SetWindowTitle("Realtime Raytracer (Go + WebGPU)")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
