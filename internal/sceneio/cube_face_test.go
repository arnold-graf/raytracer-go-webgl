package sceneio

import (
	"testing"

	"raytracer/internal/texture"
)

func TestCubeIncludeFaceTextures(t *testing.T) {
	s, err := Load(repoFile("scenes/office-sunset/server-room-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, bx := range s.Boxes {
		for fi, id := range bx.FaceTex {
			if texture.IsCapture(id) {
				n++
				t.Logf("box min=%v max=%v face %d tex %d", bx.Min, bx.Max, fi, id)
			}
		}
	}
	if n < 5 {
		t.Fatalf("capture face textures = %d, want at least 5 cube walls", n)
	}
}
