package webgpu

import (
	"path/filepath"
	"testing"

	"raytracer/internal/sceneio"
	"raytracer/internal/texture"
)

func TestCubeWallFaceTexturesPackedWithPrims(t *testing.T) {
	root := filepath.Join("..", "..")
	s, err := sceneio.Load(filepath.Join(root, "scenes", "office-sunset", "server-room-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	prims, ok := PackInstancedScene(s)
	if !ok {
		t.Fatal("PackInstancedScene failed")
	}
	faces := PackSceneFaceTextures(s, prims)
	if len(faces) != len(prims)*BoxFacesPerPrim {
		t.Fatalf("face slots = %d, want %d (prims=%d)", len(faces), len(prims)*BoxFacesPerPrim, len(prims))
	}

	static, ok := s.StaticPrimitiveCounts()
	if !ok {
		t.Fatal("StaticPrimitiveCounts")
	}
	staticScene := sliceStaticScene(s, static)

	idx := 0
	for range staticScene.Spheres {
		idx++
	}
	for range staticScene.Planes {
		idx++
	}

	var captureBoxes int
	for bi, bx := range staticScene.Boxes {
		hasCapture := false
		for _, ft := range bx.FaceTex {
			if texture.IsCapture(ft) {
				hasCapture = true
				break
			}
		}
		if hasCapture {
			captureBoxes++
			base := idx * BoxFacesPerPrim
			for fi, ft := range bx.FaceTex {
				if !texture.IsCapture(ft) {
					continue
				}
				if faces[base+fi] != uint32(ft) {
					t.Fatalf("static box %d face %d: packed %d want %d (gpu prim %d)", bi, fi, faces[base+fi], ft, idx)
				}
			}
		}
		idx++
	}
	if captureBoxes < 5 {
		t.Fatalf("capture boxes = %d, want at least 5 cube walls", captureBoxes)
	}
}
