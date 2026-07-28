package npc

import (
	"math"
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
)

func TestSpiderStepSceneKeepsUpright(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "npc-spider-test.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	maxRoll, maxPitch := 0.0, 0.0
	maxRollZ, maxPitchZ := 0.0, 0.0
	for i := 0; i < 600; i++ {
		m.Update(sc, world, 1.0/60.0)
		a := &m.agents[0]
		r := math.Abs(a.SpiderBody().Body.Roll)
		p := math.Abs(a.SpiderBody().Body.Pitch)
		if r > maxRoll {
			maxRoll = r
			maxRollZ = a.SpiderBody().Body.Pos.Z
		}
		if p > maxPitch {
			maxPitch = p
			maxPitchZ = a.SpiderBody().Body.Pos.Z
		}
	}
	t.Logf("max pitch=%.1f at z=%.2f max roll=%.1f at z=%.2f", maxPitch, maxPitchZ, maxRoll, maxRollZ)
	const maxRollDeg = 15.0
	const maxPitchDeg = 35.0
	if maxRoll > maxRollDeg {
		t.Fatalf("roll %.1f° exceeds %.1f° near step (z=%.2f)", maxRoll, maxRollDeg, maxRollZ)
	}
	if maxPitch > maxPitchDeg {
		t.Fatalf("pitch %.1f° exceeds %.1f° near step (z=%.2f)", maxPitch, maxPitchDeg, maxPitchZ)
	}
}
