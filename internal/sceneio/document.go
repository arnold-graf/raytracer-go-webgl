package sceneio

import (
	"fmt"
	"path/filepath"

	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

type documentInteractDTO struct {
	Hint  string  `toml:"hint"`
	Range float64 `toml:"use_range"`
	OnUse string  `toml:"on_use"`
}

type documentDTO struct {
	ID         string               `toml:"id"`
	PosX       float64              `toml:"pos_x"`
	PosY       float64              `toml:"pos_y"`
	PosZ       float64              `toml:"pos_z"`
	Width      float64              `toml:"width"`
	Height     float64              `toml:"height"`
	Depth      float64              `toml:"depth"`
	Headline   string               `toml:"headline"`
	Paragraphs []string             `toml:"paragraphs"`
	Font       string               `toml:"font"`
	FontSizePx int                  `toml:"font_size_px"`
	Albedo     vec3                 `toml:"albedo"`
	OnUse      string               `toml:"on_use"`
	Interact   *documentInteractDTO `toml:"interact"`
	transformDTO
}

func (d documentDTO) onUse() string {
	if d.Interact != nil && d.Interact.OnUse != "" {
		return d.Interact.OnUse
	}
	return d.OnUse
}

func (d documentDTO) build(parentDir string, slot int) (scene.DocumentSpec, error) {
	id := d.ID
	if id == "" {
		id = fmt.Sprintf("document_%d", slot)
	}
	w := d.Width
	if w <= 0 {
		w = 0.21
	}
	h := d.Height
	if h <= 0 {
		h = 0.297
	}
	dep := d.Depth
	if dep <= 0 {
		dep = 0.002
	}
	font := d.Font
	if font == "" {
		font = "assets/PixelOperator.ttf"
	}
	if !filepath.IsAbs(font) {
		font = filepath.Join(parentDir, font)
	}
	paras := texture.NormalizeParagraphs(d.Paragraphs)

	pos := vec.New(d.PosX, d.PosY, d.PosZ)
	rest := scene.DocumentRestTransform(pos, w, h, dep, d.RotateX, d.RotateY, d.RotateZ, nil)
	center := rest.ToWorld(vec.V{})
	ia := scene.Interactable{
		Hint:       "press {{use_button}} to read",
		Handler:    "document",
		Range:      useRangeDefault(d.Interact),
		Center:     center,
		DocumentID: id,
	}
	if d.Interact != nil {
		if d.Interact.Hint != "" {
			ia.Hint = d.Interact.Hint
		}
		if d.Interact.Range > 0 {
			ia.Range = d.Interact.Range
		}
	}

	return scene.DocumentSpec{
		ID:         id,
		PosX:       d.PosX,
		PosY:       d.PosY,
		PosZ:       d.PosZ,
		Width:      w,
		Height:     h,
		Depth:      dep,
		RotateX:    d.RotateX,
		RotateY:    d.RotateY,
		RotateZ:    d.RotateZ,
		Headline:   d.Headline,
		Paragraphs: paras,
		Font:       font,
		FontSizePx: d.FontSizePx,
		Albedo:     tintOrWhite(d.Albedo),
		OnUse:      d.onUse(),
		Rest:       rest,
		Interact:   &ia,
	}, nil
}

func useRangeDefault(d *documentInteractDTO) float64 {
	if d != nil && d.Range > 0 {
		return d.Range
	}
	return 1.2
}

func resolveDocuments(s *scene.Scene, docs []documentDTO, parentDir string) error {
	slot := len(s.DocumentSpecs)
	for i, d := range docs {
		spec, err := d.build(parentDir, slot+i)
		if err != nil {
			return fmt.Errorf("document[%d]: %w", i, err)
		}
		s.DocumentSpecs = append(s.DocumentSpecs, spec)
		if spec.Interact != nil {
			s.Interactables = append(s.Interactables, *spec.Interact)
		}
	}
	return nil
}

func finalizeDocuments(s *scene.Scene) error {
	imgs := map[int]*texture.CaptureImage{}
	for i := range s.DocumentSpecs {
		spec := &s.DocumentSpecs[i]
		if i >= texture.DocumentCount {
			return fmt.Errorf("too many documents (max %d)", texture.DocumentCount)
		}
		texID := texture.DocumentBase + i
		img, err := texture.RasterizeDocument(spec.Headline, spec.Paragraphs, spec.Font, spec.FontSizePx)
		if err != nil {
			return fmt.Errorf("document %q: %w", spec.ID, err)
		}
		imgs[texID] = img
		spec.TexID = texID
	}
	texture.CommitDocuments(imgs)
	return nil
}

func mergeDocumentSpecs(dst *scene.Scene, sub *scene.Scene, xf *scene.Transform) {
	for _, ds := range sub.DocumentSpecs {
		spec := ds
		spec.TexID = 0
		spec.Rest = xf.Compose(ds.Rest)
		if spec.Interact != nil {
			ia := *spec.Interact
			ia.Center = spec.Rest.ToWorld(vec.V{})
			spec.Interact = &ia
		}
		dst.DocumentSpecs = append(dst.DocumentSpecs, spec)
	}
}
