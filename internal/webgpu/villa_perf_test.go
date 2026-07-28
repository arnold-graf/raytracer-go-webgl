package webgpu

import (
	"path/filepath"
	"testing"
	"time"

	"raytracer/internal/camera"
	"raytracer/internal/npc"
	"raytracer/internal/probe"
	"raytracer/internal/render"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
)

func villaSceneWithSpider(t *testing.T) (*scene.Scene, *npc.Manager, *render.View) {
	t.Helper()
	sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := npc.NewManager()
	if err := m.Instantiate(sc, npc.FootWorld(sc)); err != nil {
		t.Fatal(err)
	}
	aoData, aoOK := probe.New(sc).BakeAO()
	view := &render.View{
		Scene:  sc,
		Shadow: true,
		Mirror: true,
		AO:     true,
		AOData: aoData,
		AOok:   aoOK,
	}
	return sc, m, view
}

func TestVillaAnimatedRenderCost(t *testing.T) {
	sc, m, view := villaSceneWithSpider(t)
	r, err := New(400, 250)
	if err != nil {
		t.Skip(err)
	}
	defer r.Release()
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	buf := make([]byte, 400*250*4)
	world := npc.FootWorld(sc)
	dt := 1.0 / 60.0

	var npcMS, renderMS float64
	const frames = 60
	for i := 0; i < frames; i++ {
		t0 := time.Now()
		m.Update(sc, world, dt)
		npcMS += time.Since(t0).Seconds() * 1000

		t0 = time.Now()
		r.Render(buf, cam, view, 1)
		renderMS += time.Since(t0).Seconds() * 1000
	}
	t.Logf("animated %d frames @400x250:", frames)
	t.Logf("  npc.Update avg: %.2f ms/frame", npcMS/float64(frames))
	t.Logf("  Render avg:     %.2f ms/frame (%.0f fps)", renderMS/float64(frames), 1000/(renderMS/float64(frames)))
	t.Logf("  total CPU+GPU:  %.2f ms/frame", (npcMS+renderMS)/float64(frames))
	last := r.LastPhaseTimings()
	t.Logf("  last frame phases: pack=%.1f upload=%.1f gpu=%.1f ms", last.Pack, last.Upload, last.GPU)
	t.Logf("  scene prims after spawn: %d spheres %d cylinders (dynamic bodies=%d)",
		len(sc.Spheres), len(sc.Cylinders), len(sc.DynamicBodies))
}

func BenchmarkVillaRenderAnimatedNPC(b *testing.B) {
	sc, m, view := villaSceneWithSpider(&testing.T{})
	r, err := New(400, 250)
	if err != nil {
		b.Skip(err)
	}
	defer r.Release()
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	buf := make([]byte, 400*250*4)
	world := npc.FootWorld(sc)
	dt := 1.0 / 60.0
	for i := 0; i < 5; i++ {
		m.Update(sc, world, dt)
		r.Render(buf, cam, view, 1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(sc, world, dt)
		r.Render(buf, cam, view, 1)
	}
}

func BenchmarkVillaBVHRefitOnly(b *testing.B) {
	sc, m, view := villaSceneWithSpider(&testing.T{})
	var c sceneCache
	c.rebuild(view)
	world := npc.FootWorld(sc)
	dt := 1.0 / 60.0
	for i := 0; i < 5; i++ {
		m.Update(sc, world, dt)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(sc, world, dt)
		sc.TouchTransforms()
		c.updateDynamicTransforms(sc)
	}
}
