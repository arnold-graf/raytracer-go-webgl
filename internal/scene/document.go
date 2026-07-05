package scene

import "math"

import "raytracer/internal/vec"

// DocumentSpec is a readable paper object loaded from [[document]] in scene TOML.
// Runtime pose and interaction state live in document.Manager.
type DocumentSpec struct {
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
	TexID      int // dynamic document texture slot (texture.DocumentBase+)
	OnUse      string // optional handler id invoked when the player opens the document
	Rest       *Transform
	Interact   *Interactable
}

// DocumentRestTransform returns the pose for a centered paper box whose geometry
// spans [-w/2,w/2] etc. pos is the anchor corner; after rotation the lowest point
// of the box rests at pos.Y so flat and upright placements share the same surface Y.
func DocumentRestTransform(pos vec.V, w, h, d, rx, ry, rz float64, xf *Transform) *Transform {
	half := vec.New(w/2, h/2, d/2)
	floor := lowestRotatedY(half, rx, ry, rz)
	center := vec.New(pos.X+w/2, pos.Y-floor, pos.Z+d/2)
	local := NewRigidTransform(rx, ry, rz, center)
	if xf == nil {
		return local
	}
	return xf.Compose(local)
}

func lowestRotatedY(half vec.V, rx, ry, rz float64) float64 {
	rot := rotation(rx, ry, rz)
	minY := math.Inf(1)
	for _, sx := range [2]float64{-1, 1} {
		for _, sy := range [2]float64{-1, 1} {
			for _, sz := range [2]float64{-1, 1} {
				c := rot.mul(vec.New(sx*half.X, sy*half.Y, sz*half.Z))
				if c.Y < minY {
					minY = c.Y
				}
			}
		}
	}
	return minY
}
