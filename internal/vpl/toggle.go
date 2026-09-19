package vpl

import (
	"sync"

	"raytracer/internal/scene"
)

// Runtime attach/detach, so the app can put the bounce on and off a key and see
// the difference on one frame instead of two runs.
//
// The set is generated once per scene and kept: regenerating on every press
// would cost a photon trace each time, and worse, would make the two states
// incomparable if anything about generation were ever made non-deterministic.
//
// Detaching truncates rather than filtering, which is safe because Attach only
// ever appends and nothing else adds lights while a scene is loaded. Scene
// reload builds a new *scene.Scene, so it lands on a fresh entry here.
type set struct {
	lights []scene.Light
	base   int // len(s.Lights) before the set was appended
	on     bool
}

var (
	setsMu sync.Mutex
	sets   = map[*scene.Scene]*set{}
)

// register records a set as attached, so a later Toggle knows how to take it off
// again. Called by Inject.
func register(s *scene.Scene, lights []scene.Light, base int) {
	setsMu.Lock()
	defer setsMu.Unlock()
	sets[s] = &set{lights: lights, base: base, on: true}
}

// Toggle attaches or detaches the scene's VPLs and reports the new state and how
// many lights are involved. The first call on a scene generates the set if
// RAYTRACER_VPL never ran, so the key works whether or not the flag was set at
// launch.
func Toggle(s *scene.Scene) (on bool, n int) {
	if s == nil {
		return false, 0
	}
	setsMu.Lock()
	st := sets[s]
	setsMu.Unlock()

	if st == nil {
		// Inject has already registered a set for any scene that had work to do,
		// so reaching here means the scene declared no [vpl] region and no
		// global budget was asked for — and in that case the key does nothing.
		//
		// It used to fall back to DefaultOptions and invent a scene-wide set, so
		// that the key would demonstrate the bounce on a scene that had never
		// opted in. That made it the one path that could place lights nobody
		// asked for: on a scene with no [vpl] anywhere and no flag, pressing 9
		// conjured twenty-four globally-ranked VPLs, which is exactly what the
		// launch path exists to prevent. The key toggles what the scene asked
		// for; RAYTRACER_VPL=24 at launch is how you ask for the rest.
		o, ok := optionsFor(s)
		if !ok {
			return false, 0
		}
		lights := Generate(s, o)
		if len(lights) == 0 {
			return false, 0
		}
		st = &set{lights: lights, base: len(s.Lights)}
		setsMu.Lock()
		sets[s] = st
		setsMu.Unlock()
	}

	if st.on {
		if len(s.Lights) >= st.base {
			s.Lights = s.Lights[:st.base]
		}
		st.on = false
	} else {
		st.base = len(s.Lights)
		s.Lights = append(s.Lights, st.lights...)
		st.on = true
	}
	// Bump the geometry generation so the renderer re-packs and re-uploads the
	// light buffer and rebuilds the clustered light grid.
	s.Touch()
	return st.on, len(st.lights)
}

// Active reports whether a scene currently has its VPLs attached.
func Active(s *scene.Scene) bool {
	setsMu.Lock()
	defer setsMu.Unlock()
	st := sets[s]
	return st != nil && st.on
}
