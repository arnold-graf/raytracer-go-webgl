package character

import "raytracer/internal/vec"

// FootWorld answers terrain queries for foot placement and grounding.
type FootWorld interface {
	GroundHeight(x, z, headY float64) float64
	GroundNormal(x, z, headY float64) vec.V
}
