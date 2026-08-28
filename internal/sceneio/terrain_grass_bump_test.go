package sceneio

import "testing"

func TestTerrainGrassBump(t *testing.T) {
	s, err := Decode([]byte(`
[[terrain]]
origin = [0, 0, 0]
size = [10, 10]
grass_bump = 0.4
grass_texture_scale = 0.15
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Terrains) != 1 {
		t.Fatalf("terrains = %d, want 1", len(s.Terrains))
	}
	if s.Terrains[0].GrassBump != 0.4 {
		t.Fatalf("grass_bump = %v, want 0.4", s.Terrains[0].GrassBump)
	}
	if s.Terrains[0].GrassTextureScale != 0.15 {
		t.Fatalf("grass_texture_scale = %v, want 0.15", s.Terrains[0].GrassTextureScale)
	}
}
