// Package render defines the backend contract the app draws through. The only
// implementation is the WebGPU renderer (internal/webgpu); this package holds
// the renderer interface plus the small, backend-agnostic types it exchanges
// with the app (the per-frame View, profiling timings, and the backend name).
package render

import (
	"raytracer/internal/camera"
	"raytracer/internal/probe"
	"raytracer/internal/scene"
)

// View is one frame's worth of scene state handed to the renderer: the scene to
// draw, the animation clock, the feature toggles the GPU honors per frame, and
// the baked ambient-occlusion volume (computed once on the CPU and uploaded by
// the backend). It carries no camera; that is passed separately so the same
// View can be reused while the camera moves.
type View struct {
	Scene  *scene.Scene
	Time   float64 // animation clock in seconds (e.g. water ripples)
	Shadow bool    // cast shadow rays
	Mirror bool    // trace mirror/glass reflections
	AO     bool    // apply the baked ambient-occlusion volume

	// AOData is the baked ambient-occlusion volume; AOok is false when the scene
	// has no finite geometry to occlude against. The backend uploads it only
	// when both AOok and the AO toggle are set.
	AOData    probe.AOData
	AOok      bool
	AOVersion uint64 // bumps when AOData is replaced (e.g. after an async bake)

	// ColorQuant selects the post-dither color depth: 0 = 8-bit, 1 = 15-bit, 2 = 256 (3-3-2).
	ColorQuant uint32
}

// Renderer is the drawing backend the app depends on. Render fills buf
// (len = W*H*4, RGBA) by rendering v from cam. pixSize is a quality/speed knob
// (1 = full resolution) that backends may honor or ignore.
type Renderer interface {
	Render(buf []byte, cam *camera.Camera, v *View, pixSize int)
}

// PhaseTimings breaks one WebGPU frame into pack/upload/shade/readback phases
// (milliseconds). Only the WebGPU backend implements PhaseTimingsProvider.
type PhaseTimings struct {
	Pack, Upload, GPU, Readback, Total float64
	Prims, Blockers, BVHNodes, Holes   int
}

// PhaseTimingsProvider is optionally implemented by the renderer so the HUD can
// show a live per-phase breakdown without importing the backend package.
type PhaseTimingsProvider interface {
	LastPhaseTimings() PhaseTimings
}

// BackendNamer is optionally implemented by a renderer to report its backend
// name for the HUD (e.g. "webgpu").
type BackendNamer interface {
	BackendName() string
}
