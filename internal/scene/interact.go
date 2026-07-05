package scene

import (
	"math"

	"raytracer/internal/vec"
)

// Interactable is a world-space trigger the player can use when nearby.
type Interactable struct {
	Hint    string
	Handler string
	Range   float64
	Center  vec.V
	DoorID     string // links on_use=door to a [[door]] id
	DocumentID string // links on_use=document to a [[document]] id
}

// NearestInteractable returns the closest interactable within its use range
// of pos, or nil if none.
func (s *Scene) NearestInteractable(pos vec.V) *Interactable {
	if s == nil || len(s.Interactables) == 0 {
		return nil
	}
	var best *Interactable
	bestD2 := math.Inf(1)
	for i := range s.Interactables {
		ia := &s.Interactables[i]
		r := ia.Range
		if r <= 0 {
			r = 1.5
		}
		dx := pos.X - ia.Center.X
		dy := pos.Y - ia.Center.Y
		dz := pos.Z - ia.Center.Z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= r*r && d2 < bestD2 {
			best = ia
			bestD2 = d2
		}
	}
	return best
}
