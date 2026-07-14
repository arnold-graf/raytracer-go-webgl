package sceneio

import (
	"fmt"

	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

type documentDTO struct {
	ID         string   `toml:"id"`
	PosX       float64  `toml:"pos_x"`
	PosY       float64  `toml:"pos_y"`
	PosZ       float64  `toml:"pos_z"`
	Width      float64  `toml:"width"`
	Height     float64  `toml:"height"`
	Depth      float64  `toml:"depth"`
	Headline   string   `toml:"headline"`
	Paragraphs []string `toml:"paragraphs"`
	Font       string   `toml:"font"`
	FontSizePx int      `toml:"font_size_px"`
	Albedo     vec3     `toml:"albedo"`
	interactPropsDTO
	transformDTO
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
	font, err := resolveFontPath(d.Font)
	if err != nil {
		return scene.DocumentSpec{}, err
	}
	paras := texture.NormalizeParagraphs(d.Paragraphs)

	pos := vec.New(d.PosX, d.PosY, d.PosZ)
	rest := scene.DocumentRestTransform(pos, w, h, dep, d.RotateX, d.RotateY, d.RotateZ, nil)
	hint := d.Hint
	if hint == "" {
		hint = "press {{use_button}} to read"
	}
	useRange := d.Range
	if useRange <= 0 {
		useRange = 1.2
	}
	ia := scene.Interactable{
		Hint:       hint,
		Handler:    "document",
		Range:      useRange,
		DocumentID: id,
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
		OnUse:      d.OnUse,
		Rest:       rest,
		Interact:   &ia,
	}, nil
}

func resolveDocuments(s *scene.Scene, docs []documentDTO, parentDir string) error {
	slot := len(s.DocumentSpecs)
	for i, d := range docs {
		spec, err := d.build(parentDir, slot+i)
		if err != nil {
			return fmt.Errorf("document[%d]: %w", i, err)
		}
		s.DocumentSpecs = append(s.DocumentSpecs, spec)
	}
	return nil
}

func finalizeDocuments(s *scene.Scene) error {
	return finalizeDynamicTextures(s)
}

func mergeDocumentSpecs(dst *scene.Scene, sub *scene.Scene, xf *scene.Transform) {
	for _, ds := range sub.DocumentSpecs {
		spec := ds
		spec.TexID = 0
		spec.Rest = xf.Compose(ds.Rest)
		dst.DocumentSpecs = append(dst.DocumentSpecs, spec)
	}
}
