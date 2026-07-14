package sceneio

import (
	"fmt"

	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

func finalizeDynamicTextures(s *scene.Scene) error {
	imgs := map[int]*texture.CaptureImage{}
	slot := 0
	for i := range s.DocumentSpecs {
		spec := &s.DocumentSpecs[i]
		if slot >= texture.DocumentCount {
			return fmt.Errorf("too many dynamic text textures (max %d)", texture.DocumentCount)
		}
		texID := texture.DocumentBase + slot
		img, err := texture.RasterizeDocument(spec.Headline, spec.Paragraphs, spec.Font, spec.FontSizePx)
		if err != nil {
			return fmt.Errorf("document %q: %w", spec.ID, err)
		}
		imgs[texID] = img
		spec.TexID = texID
		slot++
	}
	for i := range s.ScreenSpecs {
		spec := &s.ScreenSpecs[i]
		if slot >= texture.DocumentCount {
			return fmt.Errorf("too many dynamic text textures (max %d)", texture.DocumentCount)
		}
		texID := texture.DocumentBase + slot
		bg := spec.Albedo
		if bg == (vec.V{}) {
			bg = vec.New(0.05, 0.06, 0.09)
		}
		img, err := texture.RasterizeScreen(spec.Headline, spec.Paragraphs, spec.Font, spec.FontSizePx, bg, spec.FontColor)
		if err != nil {
			return fmt.Errorf("screen %q: %w", spec.ID, err)
		}
		imgs[texID] = img
		spec.TexID = texID
		slot++
	}
	texture.CommitDocuments(imgs)
	return nil
}
