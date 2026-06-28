package app

import (
	"fmt"
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"raytracer/internal/character"
	"raytracer/internal/npc"
)

func (g *Game) handleNPCDebugKeys() {
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit6) {
		g.npcDebug = !g.npcDebug
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyP) {
		g.dumpNPCPoses()
	}
}

func (g *Game) drawNPCDebug(screen *ebiten.Image) {
	if !g.npcDebug || g.npcs == nil {
		return
	}
	lines := g.npcs.DebugLines(npc.FootWorld(g.sc))
	for _, ln := range lines {
		x0, y0, ok0 := g.cam.ProjectPoint(g.rw, g.rh, ln.From)
		x1, y1, ok1 := g.cam.ProjectPoint(g.rw, g.rh, ln.To)
		if !ok0 || !ok1 {
			continue
		}
		vector.StrokeLine(screen, float32(x0), float32(y0), float32(x1), float32(y1), 1.5, debugLineColor(ln.Kind), true)
	}
}

func (g *Game) dumpNPCPoses() {
	if g.npcs == nil {
		return
	}
	frame := int(g.elapsed * 60)
	var recs []character.PoseRecord
	g.npcs.DumpCurrentPoses(frame, npc.FootWorld(g.sc), &recs)
	path := fmt.Sprintf("npc_pose_%d.jsonl", frame)
	reportPath := fmt.Sprintf("npc_pose_%d.report.txt", frame)
	if err := npc.WritePoseRecordsToFile(path, recs); err != nil {
		g.reloadMsg = fmt.Sprintf("pose dump failed: %v", err)
	} else if err := npc.WriteGaitReport(reportPath, recs); err != nil {
		g.reloadMsg = fmt.Sprintf("pose dump → %s (report failed: %v)", path, err)
	} else {
		g.reloadMsg = fmt.Sprintf("pose dump → %s + %s", path, reportPath)
	}
	g.reloadMsgAt = time.Now()
}

func debugLineColor(kind string) color.RGBA {
	switch kind {
	case "foot":
		return color.RGBA{80, 220, 120, 220}
	case "target":
		return color.RGBA{240, 200, 80, 200}
	case "travel":
		return color.RGBA{255, 120, 180, 230}
	case "ground":
		return color.RGBA{200, 200, 200, 180}
	default:
		return color.RGBA{120, 180, 255, 220}
	}
}
