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
	BoxIndex    int     // index into Scene.Boxes; set by PickInteractable
	SphereIndex int     // index into Scene.Spheres; set by PickInteractable
	LightIndex  int     // index into Scene.Lights; set by PickInteractable
	DoorID     string  // links handler "door" to a [[door]] id
	DocumentID string  // links handler "document" to a [[document]] id
	ScreenID   string  // links handler "screen" to a [[screen]] id
	StateAction string // state mutation expression when Handler == "state"
	index      int     // index in Scene.Interactables; set by RegisterInteractable
}

// Index returns this interactable's index in Scene.Interactables.
func (ia *Interactable) Index() int {
	if ia == nil {
		return -1
	}
	return ia.index
}

// RegisterInteractable appends ia and returns its index in Interactables.
func (s *Scene) RegisterInteractable(ia Interactable) int {
	ia.index = len(s.Interactables)
	s.Interactables = append(s.Interactables, ia)
	return ia.index
}

// SetBoxInteract maps a box index to an interactable index for ray picking.
func (s *Scene) SetBoxInteract(boxIdx, iaIdx int) {
	if s.boxInteract == nil {
		s.boxInteract = make(map[int]int)
	}
	s.boxInteract[boxIdx] = iaIdx
}

// SetSphereInteract maps a sphere index to an interactable index for ray picking.
func (s *Scene) SetSphereInteract(sphereIdx, iaIdx int) {
	if s.sphereInteract == nil {
		s.sphereInteract = make(map[int]int)
	}
	s.sphereInteract[sphereIdx] = iaIdx
}

// SetLightInteract maps a light index to an interactable index for ray picking.
func (s *Scene) SetLightInteract(lightIdx, iaIdx int) {
	if s.lightInteract == nil {
		s.lightInteract = make(map[int]int)
	}
	s.lightInteract[lightIdx] = iaIdx
}

// LightInteractIndex returns the interactable index registered for lightIdx.
func (s *Scene) LightInteractIndex(lightIdx int) (int, bool) {
	if s == nil || s.lightInteract == nil {
		return 0, false
	}
	ia, ok := s.lightInteract[lightIdx]
	return ia, ok
}

// InteractableSphereIndex returns the sphere index used to pick iaIdx, if any.
func (s *Scene) InteractableSphereIndex(iaIdx int) (sphereIdx int, ok bool) {
	if s == nil || s.sphereInteract == nil {
		return -1, false
	}
	for sphereIdx, ia := range s.sphereInteract {
		if ia == iaIdx {
			return sphereIdx, true
		}
	}
	return -1, false
}

// InteractBindingOffsets maps local primitive and interactable indices onto a parent scene.
type InteractBindingOffsets struct {
	Boxes, Spheres, Lights, Interactables int
}

// ApplyInteractBindings registers pick targets from local onto dst using off.
// When iaSpan is non-nil, only interactables within [iaSpan[0], iaSpan[1]) are wired.
func (s *Scene) ApplyInteractBindings(local *Scene, off InteractBindingOffsets, iaSpan *[2]int) {
	if s == nil || local == nil {
		return
	}
	for localBox, localIA := range local.boxInteract {
		if localIA < 0 || localIA >= len(local.Interactables) {
			continue
		}
		iaIdx := off.Interactables + localIA
		if iaSpan != nil && (iaIdx < iaSpan[0] || iaIdx >= iaSpan[1]) {
			continue
		}
		s.SetBoxInteract(off.Boxes+localBox, iaIdx)
	}
	for localSphere, localIA := range local.sphereInteract {
		if localIA < 0 || localIA >= len(local.Interactables) {
			continue
		}
		iaIdx := off.Interactables + localIA
		if iaSpan != nil && (iaIdx < iaSpan[0] || iaIdx >= iaSpan[1]) {
			continue
		}
		s.SetSphereInteract(off.Spheres+localSphere, iaIdx)
	}
	for localLight, localIA := range local.lightInteract {
		if localIA < 0 || localIA >= len(local.Interactables) {
			continue
		}
		iaIdx := off.Interactables + localIA
		if iaSpan != nil && (iaIdx < iaSpan[0] || iaIdx >= iaSpan[1]) {
			continue
		}
		s.SetLightInteract(off.Lights+localLight, iaIdx)
	}
}

// MergeInteractables appends sub's interactables and remaps box/light links after a merge.
func (s *Scene) MergeInteractables(sub *Scene, boxOffset int) {
	if sub == nil || len(sub.Interactables) == 0 {
		return
	}
	iaOffset := len(s.Interactables)
	s.Interactables = append(s.Interactables, sub.Interactables...)
	for i := iaOffset; i < len(s.Interactables); i++ {
		s.Interactables[i].index = i
	}
	s.ApplyInteractBindings(sub, InteractBindingOffsets{
		Boxes:         boxOffset,
		Spheres:       len(s.Spheres) - len(sub.Spheres),
		Lights:        len(s.Lights) - len(sub.Lights),
		Interactables: iaOffset,
	}, nil)
}

// PickInteractable returns the interactable on the nearest box or interactive
// light hit along ray, or nil.
func (s *Scene) PickInteractable(ray vec.Ray) *Interactable {
	if s == nil {
		return nil
	}
	bestIA := -1
	bestBox := -1
	bestSphere := -1
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
			bestSphere = -1
			bestLight = -1
		}
	}
	if len(s.sphereInteract) > 0 {
		for sphereIdx, iaIdx := range s.sphereInteract {
			if sphereIdx < 0 || sphereIdx >= len(s.Spheres) {
				continue
			}
			t := interactSphereHit(&s.Spheres[sphereIdx], ray)
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
			bestSphere = sphereIdx
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
			bestSphere = -1
			bestLight = lightIdx
		}
	}
	if bestIA < 0 {
		return nil
	}
	ia := &s.Interactables[bestIA]
	ia.BoxIndex = bestBox
	ia.SphereIndex = bestSphere
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

func interactSphereHit(sp *Sphere, ray vec.Ray) float64 {
	if sp == nil {
		return Inf
	}
	lr := ray
	if sp.Xform != nil {
		lr = sp.Xform.LocalRay(ray)
	}
	s := Sphere{Center: sp.Center, Radius: sp.Radius + interactPickMargin}
	return s.Intersect(lr)
}

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
