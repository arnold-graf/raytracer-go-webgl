package sceneio_test

import (
	"testing"

	"raytracer/internal/sceneio"
)

func TestTextureNormalMapFlag(t *testing.T) {
	s, err := sceneio.Decode([]byte(`
[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
material = "diffuse"
texture = "stone_wall"
texture_normal_map = true
albedo = [1, 1, 1]
`))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Boxes[0].TextureNormalMap {
		t.Fatal("expected texture_normal_map on box surface")
	}
}

func TestPrimitiveTextureNormalBump(t *testing.T) {
	s, err := sceneio.Decode([]byte(`
[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
material = "diffuse"
texture = "stone_wall"
texture_normal_map = true
texture_normal_bump = 0.35
albedo = [1, 1, 1]
`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Boxes[0].TextureNormalBump != 0.35 {
		t.Fatalf("texture_normal_bump = %v, want 0.35", s.Boxes[0].TextureNormalBump)
	}
}

func TestPrimitiveTextureScale(t *testing.T) {
	s, err := sceneio.Decode([]byte(`
[[box]]
pos_x = 0
pos_y = 0
pos_z = 0
width = 1
height = 1
depth = 1
material = "diffuse"
texture = "parquet_floor"
texture_scale = 0.5
albedo = [1, 1, 1]
`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Boxes[0].TextureScale != 0.5 {
		t.Fatalf("texture_scale = %v, want 0.5", s.Boxes[0].TextureScale)
	}
}
