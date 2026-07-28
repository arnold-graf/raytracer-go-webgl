package npc

import (
	"path/filepath"
	"testing"
	"time"

	"raytracer/internal/sceneio"
)

func TestVillaNPCUpdateCost(t *testing.T) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		t.Fatal(err)
	}
	dt := 1.0 / 60.0
	const frames = 600
	start := time.Now()
	for i := 0; i < frames; i++ {
		m.Update(sc, world, dt)
	}
	elapsed := time.Since(start)
	perFrame := elapsed / frames
	t.Logf("npc.Update: %d frames in %v (%.2f ms/frame)", frames, elapsed, perFrame.Seconds()*1000)
}

func BenchmarkVillaNPCUpdate(b *testing.B) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		b.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		b.Fatal(err)
	}
	dt := 1.0 / 60.0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(sc, world, dt)
	}
}

func BenchmarkVillaGroundHeightSpiderFeet(b *testing.B) {
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		b.Fatal(err)
	}
	m := NewManager()
	world := FootWorld(sc)
	if err := m.Instantiate(sc, world); err != nil {
		b.Fatal(err)
	}
	a := &m.agents[0]
	dt := 1.0 / 60.0
	for i := 0; i < 60; i++ {
		m.Update(sc, world, dt)
	}
	headY := a.SpiderBody().Body.Pos.Y + a.Rig.HipHeight + 0.5
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, f := range a.SpiderBody().Feet {
			_ = world.GroundHeight(f.PlantWorld.X, f.PlantWorld.Z, headY)
		}
	}
}
