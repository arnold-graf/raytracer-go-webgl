package door

import "raytracer/internal/vec"

// AnimateEvent is emitted when a door starts opening or closing.
type AnimateEvent struct {
	ID         string
	Kind       string
	Opening    bool
	Center     vec.V
	TravelTime float64 // seconds for a full open/close cycle at current speed
}

// AnimateHook is called when door animation begins.
type AnimateHook func(AnimateEvent)
