package render

import (
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
	"raytracer/internal/trace"
)

// BenchmarkFrame measures the cost of one full-resolution frame (400x300,
// pixSize=1) with all features on, matching the GUI default. Divide 1s by the
// reported seconds/op to estimate the achievable fps.
func BenchmarkFrame(b *testing.B) {
	const w, h = 400, 300
	r := New(w, h)
	cam := camera.New()
	tr := trace.New(scene.Default())
	tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	buf := make([]byte, w*h*4)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Render(buf, cam, tr, 1)
	}
}

// BenchmarkOutdoorFrame measures one full-res frame of the terrain showcase
// scene (the heavy case: heightfield marching + sun shadows + AO).
func BenchmarkOutdoorFrame(b *testing.B) {
	const w, h = 400, 300
	sc, err := sceneio.Load("../../scenes/outdoors.toml")
	if err != nil {
		b.Fatal(err)
	}
	r := New(w, h)
	cam := camera.New()
	if sc.Start.Set {
		cam.Pos, cam.Yaw, cam.Pitch = sc.Start.Pos, sc.Start.Yaw, sc.Start.Pitch
	}
	tr := trace.New(sc)
	tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	buf := make([]byte, w*h*4)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Render(buf, cam, tr, 1)
	}
}
