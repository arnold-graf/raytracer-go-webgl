package camera

import (
	"math"
	"testing"
)

// mockWorld is a programmable World for exercising movement physics.
type mockWorld struct {
	height  func(x, z float64) float64
	blocked func(x, z float64) bool
}

func (m mockWorld) GroundHeight(x, z, headY float64) float64 {
	if m.height != nil {
		return m.height(x, z)
	}
	return 0
}

func (m mockWorld) Blocked(x, z, feetY, headY, radius, step float64) bool {
	if m.blocked != nil {
		return m.blocked(x, z)
	}
	return false
}

func TestWalkSpeedIs2_5xOriginal(t *testing.T) {
	// Original per-frame step was 0.07*dt*2 = 0.014 at dt=0.1; 2.5x = 0.035.
	c := New()
	c.SetWorld(mockWorld{})
	c.Update(Move{}, 0.1) // settle onto the ground
	z0 := c.Pos.Z
	c.Update(Move{Forward: true}, 0.1)
	got := z0 - c.Pos.Z // forward at yaw 0 moves toward -Z
	if math.Abs(got-0.035) > 1e-9 {
		t.Fatalf("forward step = %v, want 0.035", got)
	}
}

func TestJumpIsBriskAndBounded(t *testing.T) {
	c := New()
	c.SetWorld(mockWorld{})
	c.Update(Move{}, 0.1)
	base := c.Pos.Y

	c.Update(Move{Jump: true}, 0.1)
	maxY := c.Pos.Y
	frames := 1
	for i := 0; i < 600; i++ {
		c.Update(Move{}, 0.1)
		if c.Pos.Y > maxY {
			maxY = c.Pos.Y
		}
		frames++
		if c.onGround {
			break
		}
	}

	height := maxY - base
	if height < 0.30 || height > 0.60 {
		t.Fatalf("jump height = %v, want ~0.44", height)
	}
	if frames > 60 { // < ~1s of airtime at 60 Hz: brisk, not floaty
		t.Fatalf("airtime = %d frames, want < 60 (brisk)", frames)
	}
}

func TestWallCollisionStopsAndSlides(t *testing.T) {
	// A wall occupies x > 0.7 (already accounting for radius).
	world := mockWorld{blocked: func(x, z float64) bool { return x > 0.7 }}
	c := New()
	c.SetWorld(world)
	c.Update(Move{}, 0.1)

	// Walk right into the wall while also moving forward: X is capped, Z slides.
	for i := 0; i < 200; i++ {
		c.Update(Move{Right: true, Forward: true}, 0.1)
	}
	if c.Pos.X > 0.7+1e-9 {
		t.Fatalf("player passed through wall: x = %v", c.Pos.X)
	}
	if c.Pos.Z >= 0 {
		t.Fatalf("player failed to slide along wall: z = %v (expected negative)", c.Pos.Z)
	}
}

func TestNoClipPassesThroughWalls(t *testing.T) {
	world := mockWorld{blocked: func(x, z float64) bool { return x > 0.7 }}
	c := New()
	c.SetWorld(world)
	c.NoClip = true
	c.Update(Move{}, 0.1)

	for i := 0; i < 100; i++ {
		c.Update(Move{Right: true}, 0.1)
	}
	if c.Pos.X <= 0.7 {
		t.Fatalf("noclip failed to pass the wall: x = %v", c.Pos.X)
	}
}

func TestGroundFollowing(t *testing.T) {
	height := 2.0
	c := New()
	c.SetWorld(mockWorld{height: func(x, z float64) float64 { return height }})

	c.Update(Move{}, 0.1)
	if math.Abs(c.Pos.Y-(2.0+c.cfg.EyeHeight)) > 1e-9 {
		t.Fatalf("eye on raised ground = %v, want %v", c.Pos.Y, 2.0+c.cfg.EyeHeight)
	}

	// Drop the ground; the player should fall to the new surface.
	height = 0
	for i := 0; i < 200; i++ {
		c.Update(Move{}, 0.1)
		if c.onGround {
			break
		}
	}
	if math.Abs(c.Pos.Y-c.cfg.EyeHeight) > 1e-9 {
		t.Fatalf("eye after descent = %v, want %v", c.Pos.Y, c.cfg.EyeHeight)
	}
}
