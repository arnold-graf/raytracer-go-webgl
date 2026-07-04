package scene

import "raytracer/internal/vec"

// DoorSpec is a declarative swinging door loaded from [[door]] in scene TOML.
// Runtime state lives in door.Manager agents.
type DoorSpec struct {
	ID         string
	Kind       string // "single" or "double"
	Hinge      vec.V  // hinge edge in scene/object space (single / left panel)
	HingeRight vec.V  // right panel hinge for double doors
	Axis       string // "x", "y", or "z"
	ClosedAngle float64 // radians
	OpenAngle   float64 // radians (max swing)
	Swing       string  // "one_way" or "both"
	OpenSign    float64 // +1 / -1 for one_way
	Speed       float64 // rad/s
	PanelBoxes  []int   // indices into Scene.Boxes
	// PanelClosedAngles are per-panel offsets from the closed pose (radians),
	// applied about each panel's hinge. Optional; length may be less than panels.
	PanelClosedAngles []float64
	Interact    *Interactable
}

// DoorGhostBox reports whether box index i should be ignored by player Blocked().
// Updated each frame by door.Manager while panels ghost through the player.
type DoorGhostBox func(boxIndex int) bool
