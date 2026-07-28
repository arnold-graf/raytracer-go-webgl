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

func TestVillaWithVsWithoutSpider(t *testing.T) {
	load := func() (*scene.Scene, *render.View) {
		sc, err := sceneio.Load(filepath.Join("..", "..", "scenes", "outdoors-night-villa.toml"))
		if err != nil {
			t.Fatal(err)
		}
		aoData, aoOK := probe.New(sc).BakeAO()
		return sc, &render.View{
			Scene: sc, Shadow: true, Mirror: true, AO: true, AOData: aoData, AOok: aoOK,
		}
	}

	r, err := New(512, 320)
	if err != nil {
		t.Skip(err)
	}
	defer r.Release()
	buf := make([]byte, 512*320*4)

	scStatic, viewStatic := load()
	cam := camera.New()
	if scStatic.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = scStatic.Start.Pos, scStatic.Start.Yaw, scStatic.Start.Pitch
	}
	var staticMS float64
	for i := 0; i < 30; i++ {
		t0 := time.Now()
		r.Render(buf, cam, viewStatic, 1)
		staticMS += time.Since(t0).Seconds() * 1000
	}
	staticAvg := staticMS / 30
	t.Logf("villa WITHOUT npc instantiate: %.2f ms/frame (%.0f fps)", staticAvg, 1000/staticAvg)

	sc, viewAnim := load()
	m := npc.NewManager()
	if err := m.Instantiate(sc, npc.FootWorld(sc)); err != nil {
		t.Fatal(err)
	}
	world := npc.FootWorld(sc)
	dt := 1.0 / 60.0
	var animMS, npcMS float64
	for i := 0; i < 30; i++ {
		t0 := time.Now()
		m.Update(sc, world, dt)
		npcMS += time.Since(t0).Seconds() * 1000
		t0 = time.Now()
		r.Render(buf, cam, viewAnim, 1)
		animMS += time.Since(t0).Seconds() * 1000
	}
	animAvg := animMS / 30
	npcAvg := npcMS / 30
	last := r.LastPhaseTimings()
	t.Logf("villa WITH spider animated: %.2f ms/frame render (%.0f fps), npc.Update=%.2f ms",
		animAvg, 1000/animAvg, npcAvg)
	t.Logf("  render delta: +%.2f ms/frame | pack=%.1f upload=%.1f gpu=%.1f ms",
		animAvg-staticAvg, last.Pack, last.Upload, last.GPU)
	t.Logf("  +%d spheres +%d cylinders from spider rig",
		len(sc.Spheres)-len(scStatic.Spheres), len(sc.Cylinders)-len(scStatic.Cylinders))
}
