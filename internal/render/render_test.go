package render

import (
	"testing"

	"raytracer/internal/camera"
	"raytracer/internal/scene"
	"raytracer/internal/trace"
)

// TestRenderProducesImage exercises the whole pipeline (scene -> primitives ->
// trace -> camera -> render) headlessly and checks that the framebuffer is
// fully written and not uniformly black.
func TestRenderProducesImage(t *testing.T) {
	const w, h = 400, 300
	r := New(w, h)
	cam := camera.New()
	tr := trace.New(scene.Default())
	tr.Opts = trace.Options{Mirror: true, Shadow: true, AO: true}

	buf := make([]byte, w*h*4)
	r.Render(buf, cam, tr, 4)

	var nonBlack, opaque int
	for i := 0; i < len(buf); i += 4 {
		if buf[i] != 0 || buf[i+1] != 0 || buf[i+2] != 0 {
			nonBlack++
		}
		if buf[i+3] == 255 {
			opaque++
		}
	}
	if opaque != w*h {
		t.Fatalf("expected all %d pixels opaque, got %d", w*h, opaque)
	}
	if nonBlack < w*h/10 {
		t.Fatalf("image looks empty: only %d/%d non-black pixels", nonBlack, w*h)
	}
}
