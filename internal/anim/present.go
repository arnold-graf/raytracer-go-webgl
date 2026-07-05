package anim

import (
	"math"

	"raytracer/internal/scene"
)

// Channel animates an object between a rest pose and a live target pose (e.g. held
// up to the camera). While Active the target is followed once fully open; Closing
// returns from a frozen pose without requiring Active (movement can unlock immediately).
type Channel struct {
	Duration  float64
	OpenT     float64
	CloseT    float64
	Active    bool
	Closing   bool
	CloseFrom *scene.Transform
}

// Engaged reports whether the object is opening, held, or returning to rest.
func (c *Channel) Engaged() bool { return c.Active || c.Closing }

// Open begins or resumes presenting toward target.
func (c *Channel) Open() {
	c.Closing = false
	c.CloseFrom = nil
	c.CloseT = 0
	c.Active = true
}

// Close freezes from as the return start pose and begins animating back to rest.
func (c *Channel) Close(from *scene.Transform) {
	c.CloseFrom = from.Clone()
	c.Active = false
	c.Closing = true
	c.CloseT = 0
}

// Update advances the channel and returns the pose for this frame. rest is the idle
// pose; target is the live held pose (ignored while Closing). The second return is
// true when progress changed.
func (c *Channel) Update(dt float64, rest, target *scene.Transform) (*scene.Transform, bool) {
	if c.Duration <= 0 {
		c.Duration = 0.55
	}
	speed := 1.0 / c.Duration

	if c.Closing {
		pose := c.closePose(rest)
		prev := c.CloseT
		c.CloseT = math.Min(1, c.CloseT+speed*dt)
		if c.CloseT >= 1 {
			c.Closing = false
			c.CloseFrom = nil
			c.OpenT = 0
		}
		return pose, c.CloseT != prev
	}

	if c.Active {
		prev := c.OpenT
		if c.OpenT < 1 {
			c.OpenT = math.Min(1, c.OpenT+speed*dt)
		}
		var pose *scene.Transform
		if c.OpenT >= 1 {
			pose = target.Clone()
		} else {
			pose = scene.LerpTransform(rest, target, scene.SmoothStep(c.OpenT))
		}
		return pose, c.OpenT != prev
	}

	return rest.Clone(), false
}

func (c *Channel) closePose(rest *scene.Transform) *scene.Transform {
	from := c.CloseFrom
	if from == nil {
		from = rest
	}
	u := scene.SmoothStep(c.CloseT)
	return scene.LerpTransform(rest, from, 1-u)
}
