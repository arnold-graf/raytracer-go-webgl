package render

import (
	"bytes"
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/sceneio"
	"raytracer/internal/trace"
	"raytracer/internal/vec"
)

// TestTexCacheNoRegression renders the same static, texture-heavy view twice.
// The second frame is served from the per-pixel texture cache that the first
// populated. Because the cache is keyed on the exact (texture, point, base)
// inputs, the cached frame must be byte-for-byte identical to the freshly
// computed one — caching must never change the image.
func TestTexCacheNoRegression(t *testing.T) {
	sc, err := sceneio.Load("../../scenes/indoor-outdoor.toml")
	if err != nil {
		t.Fatal(err)
	}
	cam := camera.New()
	cam.Pos, cam.Yaw, cam.Pitch = vec.New(16, 1.6, 16), 0, 0.02
	tr := trace.New(sc)
	tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}
	tr.Prepare()

	const w, h = 200, 125
	ren := New(w, h)
	a := make([]byte, w*h*4)
	b := make([]byte, w*h*4)
	tr.Time = 0
	ren.Render(a, cam, tr, 1) // populate cache
	ren.Render(b, cam, tr, 1) // serve from cache, same Time
	if !bytes.Equal(a, b) {
		t.Fatal("cached frame differs from freshly-computed frame")
	}
}
