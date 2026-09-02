package scene

import "raytracer/internal/vec"

// DoorPanelGeom is half-open index ranges into scene primitive slices for one
// swinging door leaf. Populated at load time when a panel file is merged.
type DoorPanelGeom struct {
	Boxes     [2]int
	Spheres   [2]int
	Cylinders [2]int
}

// PrimaryBox returns the first box index in the panel, or -1 if the panel has
// no boxes (used for hinge-plane queries and swing clamp probes).
func (g DoorPanelGeom) PrimaryBox() int {
	if g.Boxes[0] < g.Boxes[1] {
		return g.Boxes[0]
	}
	return -1
}

// DoorSpec is a declarative door loaded from [[door]] in scene TOML.
// Runtime state lives in door.Manager agents.
type DoorSpec struct {
	ID         string
	Kind       string // "single", "double", or "sliding"
	Hinge      vec.V  // hinge edge in scene/object space (single / left panel)
	HingeRight vec.V  // right panel hinge for double doors
	Axis       string // "x", "y", or "z" (swing doors)
	ClosedAngle float64 // radians
	OpenAngle   float64 // radians (max swing)
	OpenDistance float64 // metres (sliding doors)
	SlideDir    vec.V    // unit slide axis in object space (sliding doors)
	Swing       string  // "one_way" or "both"
	OpenSign    float64 // +1 / -1 for one_way
	Speed       float64 // rad/s (swing) or m/s (slide)
	Panels      []DoorPanelGeom
	// FrameBoxes are static frame/jamb box indices (half-open) merged with the
	// door before the panel; ignored by swing collision probes.
	FrameBoxes [2]int
	// PanelClosedAngles are per-panel offsets from the closed pose (radians),
	// applied about each panel's hinge. Optional; length may be less than panels.
	PanelClosedAngles []float64
	CanClose          bool    // player may close via interact (default true)
	AutocloseTimeout  float64 // seconds open before auto-close; 0 = disabled
	Interact    *Interactable
}

// DoorGhostBox reports whether box index i should be ignored by player Blocked().
// Updated each frame by door.Manager while panels ghost through the player.
type DoorGhostBox func(boxIndex int) bool
