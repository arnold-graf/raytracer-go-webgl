package scene

import "raytracer/internal/vec"

// ScreenSpec is an interactable display surface loaded from [[screen]] in scene TOML.
// Runtime interaction state lives in screen.Manager.
type ScreenSpec struct {
	ID         string
	PosX       float64
	PosY       float64
	PosZ       float64
	Width      float64
	Height     float64
	Depth      float64
	RotateX    float64
	RotateY    float64
	RotateZ    float64
	Headline   string
	Paragraphs []string
	Font       string
	FontSizePx int
	Albedo     vec.V
	FontColor  vec.V
	Mat        int
	Rough      float64
	Reflect    float64
	TexID      int // dynamic texture slot (texture.DocumentBase+)
	OnUse      string
	Rest       *Transform
	Interact   *Interactable
}
