package sceneio

import "raytracer/internal/scene"

type interactPropsDTO struct {
	Hint  string  `toml:"hint"`
	OnUse string  `toml:"on_use"`
	Range float64 `toml:"use_range"`
}

func (p interactPropsDTO) build() scene.Interactable {
	return scene.Interactable{
		Hint:    p.Hint,
		Handler: p.OnUse,
		Range:   p.Range,
	}
}
