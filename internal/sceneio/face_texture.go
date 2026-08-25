package sceneio

import (
	"raytracer/internal/texture"
)

// faceTextureDTO holds optional per-face texture names on [[box]].
type faceTextureDTO struct {
	TextureRight   string `toml:"texture_right"`
	TextureLeft    string `toml:"texture_left"`
	TextureTop     string `toml:"texture_top"`
	TextureBottom  string `toml:"texture_bottom"`
	TextureFront   string `toml:"texture_front"`
	TextureBack    string `toml:"texture_back"`
}

func (f faceTextureDTO) resolve() ([6]int, error) {
	names := [6]string{
		f.TextureRight, f.TextureLeft, f.TextureTop,
		f.TextureBottom, f.TextureFront, f.TextureBack,
	}
	var out [6]int
	for i, name := range names {
		if name == "" {
			continue
		}
		id, _, _, err := texture.Parse(name)
		if err != nil {
			return out, err
		}
		out[i] = id
	}
	return out, nil
}
