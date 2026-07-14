package sceneio

import (
	"fmt"

	"raytracer/internal/scene"
	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

type screenDTO struct {
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
	FontColor  vec3     `toml:"font_color"`
	Material   string   `toml:"material"`
	Albedo     vec3     `toml:"albedo"`
	Rough      float64  `toml:"rough"`
	Reflect    float64  `toml:"reflect"`
	interactPropsDTO
	transformDTO
}

func (d screenDTO) build(parentDir string, slot int) (scene.ScreenSpec, error) {
	id := d.ID
	if id == "" {
		id = fmt.Sprintf("screen_%d", slot)
	}
	w := d.Width
	if w <= 0 {
		w = 0.28
	}
	h := d.Height
	if h <= 0 {
		h = 0.23
	}
	dep := d.Depth
	if dep <= 0 {
		dep = 0.002
	}
	font, err := resolveFontPath(d.Font)
	if err != nil {
		return scene.ScreenSpec{}, err
	}
	mat := scene.MatEmit
	if d.Material != "" {
		m, ok := materialByName[d.Material]
		if !ok {
			return scene.ScreenSpec{}, fmt.Errorf("unknown material %q", d.Material)
		}
		mat = m
	}
	paras := texture.NormalizeParagraphs(d.Paragraphs)

	pos := vec.New(d.PosX, d.PosY, d.PosZ)
	rest := scene.DocumentRestTransform(pos, w, h, dep, d.RotateX, d.RotateY, d.RotateZ, nil)
	hint := d.Hint
	if hint == "" {
		hint = "computer screen"
	}
	useRange := d.Range
	if useRange <= 0 {
		useRange = 1.5
	}
	ia := scene.Interactable{
		Hint:     hint,
		Handler:  "screen",
		Range:    useRange,
		ScreenID: id,
	}

	bg := d.Albedo.toV()
	if bg == (vec.V{}) {
		bg = vec.New(0.05, 0.06, 0.09)
	}
	fontCol := d.FontColor.toV()
	if fontCol == (vec.V{}) {
		fontCol = vec.New(0.82, 0.95, 1.0)
	}

	return scene.ScreenSpec{
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
		Albedo:     bg,
		FontColor:  fontCol,
		Mat:        mat,
		Rough:      d.Rough,
		Reflect:    d.Reflect,
		OnUse:      d.OnUse,
		Rest:       rest,
		Interact:   &ia,
	}, nil
}

func resolveScreens(s *scene.Scene, screens []screenDTO, parentDir string) error {
	slot := len(s.ScreenSpecs)
	for i, d := range screens {
		spec, err := d.build(parentDir, slot+i)
		if err != nil {
			return fmt.Errorf("screen[%d]: %w", i, err)
		}
		s.ScreenSpecs = append(s.ScreenSpecs, spec)
	}
	return nil
}

func mergeScreenSpecs(dst *scene.Scene, sub *scene.Scene, xf *scene.Transform) {
	for _, ss := range sub.ScreenSpecs {
		spec := ss
		spec.TexID = 0
		spec.Rest = xf.Compose(ss.Rest)
		dst.ScreenSpecs = append(dst.ScreenSpecs, spec)
	}
}
