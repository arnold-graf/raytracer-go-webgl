package scene

import "raytracer/internal/vec"

// Ambience is a looping spatial sound source placed in the world (e.g. crickets
// in a tree). The audio engine attenuates and pans it by distance and direction
// from the listener.
type Ambience struct {
	Sound  string  // sound id, e.g. "crickets"
	Pos    vec.V   // emitter position in world space
	Gain   float64 // loudness at the source (default 0.3)
	Radius float64 // distance at which the sound is inaudible (default 20)
}
