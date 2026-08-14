package scene

import (
	"raytracer/internal/vec"
)

const (
	defaultInteractRange = 3.0
	interactPickMargin   = 0.2 // extra hit volume on each side (metres)
)

// Interactable is a world-space use target bound to scene geometry or a light.
type Interactable struct {
	Hint       string
	Handler    string
	Range      float64 // max ray distance (metres) from the camera
	BoxIndex   int     // index into Scene.Boxes; set by PickInteractable
	LightIndex int     // index into Scene.Lights; set by PickInteractable
	DoorID     string  // links handler "door" to a [[door]] id
	DocumentID string  // links handler "document" to a [[document]] id
	ScreenID   string  // links handler "screen" to a [[screen]] id
}

// RegisterInteractable appends ia and returns its index in Interactables.
func (s *Scene) RegisterInteractable(ia Interactable) int {
	s.Interactables = append(s.Interactables, ia)
	return len(s.Interactables) - 1
}

// SetBoxInteract maps a box index to an interactable index for ray picking.
func (s *Scene) SetBoxInteract(boxIdx, iaIdx int) {
	if s.boxInteract == nil {
		s.boxInteract = make(map[int]int)
	}
	s.boxInteract[boxIdx] = iaIdx
}

// SetLightInteract maps a light index to an interactable index for ray picking.
func (s *Scene) SetLightInteract(lightIdx, iaIdx int) {
	if s.lightInteract == nil {
		s.lightInteract = make(map[int]int)
	}
	s.lightInteract[lightIdx] = iaIdx
}

// MergeInteractables appends sub's interactables and remaps box/light links after a merge.
func (s *Scene) MergeInteractables(sub *Scene, boxOffset int) {
	if sub == nil || len(sub.Interactables) == 0 {
		return
	}
	iaOffset := len(s.Interactables)
	s.Interactables = append(s.Interactables, sub.Interactables...)
	if len(sub.boxInteract) > 0 {
		if s.boxInteract == nil {
			s.boxInteract = make(map[int]int)
		}
		for localBox, localIA := range sub.boxInteract {
			s.boxInteract[boxOffset+localBox] = iaOffset + localIA
		}
	}
	if len(sub.lightInteract) > 0 {
		if s.lightInteract == nil {
			s.lightInteract = make(map[int]int)
		}
		lightOffset := len(s.Lights) - len(sub.Lights)
		for localLight, localIA := range sub.lightInteract {
			s.lightInteract[lightOffset+localLight] = iaOffset + localIA
		}
	}
}

// PickInteractable returns the interactable on the nearest box or interactive
// light hit along ray, or nil.
func (s *Scene) PickInteractable(ray vec.Ray) *Interactable {
	if s == nil {
		return nil
	}
	bestIA := -1
	bestBox := -1
	bestLight := -1
	bestT := Inf
	if len(s.boxInteract) > 0 {
		for boxIdx, iaIdx := range s.boxInteract {
			if boxIdx < 0 || boxIdx >= len(s.Boxes) {
				continue
			}
			b := &s.Boxes[boxIdx]
			t := interactBoxHit(b, b.Xform.LocalRay(ray))
			if t <= 0 || t >= bestT {
				continue
			}
			maxDist := interactMaxDist(s, iaIdx)
			if t > maxDist {
				continue
			}
			bestT = t
			bestIA = iaIdx
			bestBox = boxIdx
			bestLight = -1
		}
	}
	if len(s.lightInteract) > 0 {
		for lightIdx, iaIdx := range s.lightInteract {
			if lightIdx < 0 || lightIdx >= len(s.Lights) {
				continue
			}
			t := interactLightHit(&s.Lights[lightIdx], ray)
			if t <= 0 || t >= bestT {
				continue
			}
			maxDist := interactMaxDist(s, iaIdx)
			if t > maxDist {
				continue
			}
			bestT = t
			bestIA = iaIdx
			bestBox = -1
			bestLight = lightIdx
		}
	}
	if bestIA < 0 {
		return nil
	}
	ia := &s.Interactables[bestIA]
	ia.BoxIndex = bestBox
	ia.LightIndex = bestLight
	return ia
}

func interactMaxDist(s *Scene, iaIdx int) float64 {
	maxDist := defaultInteractRange
	if iaIdx >= 0 && iaIdx < len(s.Interactables) {
		if r := s.Interactables[iaIdx].Range; r > 0 {
			maxDist = r
		}
	}
	return maxDist
}

const defaultLightPickRadius = 0.12

func interactLightHit(l *Light, ray vec.Ray) float64 {
	if l == nil {
		return Inf
	}
	r := l.Radius
	if r <= 0 {
		r = defaultLightPickRadius
	}
	r += interactPickMargin
	sp := Sphere{Center: l.Pos, Radius: r}
	return sp.Intersect(ray)
}

// interactBoxHit raycasts against the box with interactPickMargin added on every
// local axis. Rendering and physics still use the tight bounds.
func interactBoxHit(b *Box, ray vec.Ray) float64 {
	if b == nil {
		return Inf
	}
	m := interactPickMargin
	expanded := *b
	expanded.Min = vec.New(b.Min.X-m, b.Min.Y-m, b.Min.Z-m)
	expanded.Max = vec.New(b.Max.X+m, b.Max.Y+m, b.Max.Z+m)
	expanded.Holes = nil
	return expanded.Intersect(ray)
}
