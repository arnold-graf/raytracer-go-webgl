package sceneio

import (
	"testing"

	"raytracer/internal/texture"
)

func TestBoxFaceTexturesLoad(t *testing.T) {
	s, err := Decode([]byte(`
[[box]]
material = "emit"
pos_x = 0
pos_y = 0
pos_z = 0
width = 2
height = 3
depth = 4
albedo = [1, 1, 1]
texture_front = "capture_forward"
texture_back = "capture_backward"
texture_top = "capture_up"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(s.Boxes))
	}
	bx := s.Boxes[0]
	if bx.FaceTex[texture.BoxFacePosZ] != texture.CaptureForward {
		t.Fatalf("front = %d, want capture_forward", bx.FaceTex[texture.BoxFacePosZ])
	}
	if bx.FaceTex[texture.BoxFaceNegZ] != texture.CaptureBackward {
		t.Fatalf("back = %d", bx.FaceTex[texture.BoxFaceNegZ])
	}
	if bx.FaceTex[texture.BoxFacePosY] != texture.CaptureUp {
		t.Fatalf("top = %d", bx.FaceTex[texture.BoxFacePosY])
	}
	if bx.Min.X != 0 || bx.Max.X != 2 || bx.Max.Y != 3 || bx.Max.Z != 4 {
		t.Fatalf("bounds min=%v max=%v", bx.Min, bx.Max)
	}
}
