package character

import (
	"math"
	"testing"

	"raytracer/internal/vec"
)

// rampGround is an infinite plane y = -rise*z (uphill when walking in -Z).
type rampGround struct {
	rise float64
}

func (g rampGround) GroundHeight(x, z, headY float64) float64 {
	_ = x
	_ = headY
	return math.Max(0, -z*g.rise)
}

func (g rampGround) GroundNormal(x, z, headY float64) vec.V {
	_ = x
	_ = z
	_ = headY
	n := vec.V{Y: 1, Z: g.rise}
	if n.LenSq() < 1e-12 {
		return vec.V{Y: 1}
	}
	return n.Normalize()
}

func (g rampGround) CastRay(origin, dir vec.V, maxDist, headY float64) SurfaceHit {
	_ = headY
	if dir.LenSq() < 1e-18 {
		return SurfaceHit{}
	}
	dir = dir.Normalize()
	denom := dir.Y + g.rise*dir.Z
	if math.Abs(denom) < 1e-9 {
		return SurfaceHit{}
	}
	t := -(origin.Y + g.rise*origin.Z) / denom
	if t < 0 || t > maxDist {
		return SurfaceHit{}
	}
	p := origin.Add(dir.Scale(t))
	return SurfaceHit{
		Point:  p,
		Normal: g.GroundNormal(p.X, p.Z, 0),
		Hit:    true,
		Dist:   t,
	}
}

func TestSpiderMoveOnSurfaceFlat(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	z0 := s.Body.Pos.Z
	y0 := s.Body.Pos.Y
	for i := 0; i < 180; i++ {
		s.Update(1.0/60.0, r, world)
	}
	if z0-s.Body.Pos.Z < 1.2 {
		t.Fatalf("flat: moved only %.2fm in Z", z0-s.Body.Pos.Z)
	}
	if math.Abs(s.Body.Pos.Y-y0) > 0.15 {
		t.Fatalf("flat: body Y drifted %.3f -> %.3f", y0, s.Body.Pos.Y)
	}
}

func TestSpiderMoveOnSurfaceRamp(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const rise = 0.45
	world := rampGround{rise: rise}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 1.0, world)
	y0 := s.Body.Pos.Y
	z0 := s.Body.Pos.Z
	for i := 0; i < 200; i++ {
		s.Update(1.0/60.0, r, world)
	}
	climb := s.Body.Pos.Y - y0
	travel := z0 - s.Body.Pos.Z
	if travel < 1.0 {
		t.Fatalf("ramp: traveled only %.2fm", travel)
	}
	wantClimb := travel * rise
	if climb < wantClimb*0.45 {
		t.Fatalf("ramp: climbed %.2fm, want >= %.2fm (rise=%.2f)", climb, wantClimb*0.45, rise)
	}
	if s.Up.Y < 0.75 {
		t.Fatalf("ramp: surface up Y=%.2f, want tilted toward slope", s.Up.Y)
	}
}

func TestSpiderGroundCheckIgnoresDistantWall(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := wallAheadGround{wallZ: 3.0, wallDist: 2.5}
	s := NewSpiderLocomotor(r, vec.V{X: 0, Z: 0}, 0, 1.0, world)
	s.Up = vec.V{Y: 1}
	s.groundCheck(world)
	if s.Up.Y < 0.9 {
		t.Fatalf("upright spider should stay on floor, Up=%v", s.Up)
	}
	if s.groundRay != groundDown {
		t.Fatalf("groundRay = %d, want down", s.groundRay)
	}
}

// wallAheadGround is a floor with a vertical wall far ahead (for groundCheck tests).
type wallAheadGround struct {
	wallZ    float64
	wallDist float64
}

func (w wallAheadGround) GroundHeight(x, z, headY float64) float64 {
	_ = x
	_ = z
	_ = headY
	return 0
}

func (w wallAheadGround) GroundNormal(x, z, headY float64) vec.V {
	_ = x
	_ = z
	_ = headY
	return vec.V{Y: 1}
}

func (w wallAheadGround) CastRay(origin, dir vec.V, maxDist, headY float64) SurfaceHit {
	_ = headY
	if dir.LenSq() < 1e-18 {
		return SurfaceHit{}
	}
	dir = dir.Normalize()
	// Floor below.
	if dir.Y < -0.5 {
		t := -origin.Y / dir.Y
		if t >= 0 && t <= maxDist {
			p := origin.Add(dir.Scale(t))
			return SurfaceHit{Point: p, Normal: vec.V{Y: 1}, Hit: true, Dist: t}
		}
	}
	// Vertical wall at z = wallZ when moving in -Z.
	if math.Abs(dir.Z) > 0.5 && dir.Z < 0 {
		t := (origin.Z - w.wallZ) / -dir.Z
		if t >= 0 && t <= maxDist {
			p := origin.Add(dir.Scale(t))
			return SurfaceHit{Point: p, Normal: vec.V{Z: 1}, Hit: true, Dist: t}
		}
	}
	return SurfaceHit{}
}

func TestSpiderSnapBodyToSurfaceHover(t *testing.T) {
	r, err := LoadRig(repoFile("data/rigs/spider.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	world := flatGround{}
	s := NewSpiderLocomotor(r, vec.V{}, 0, 0, world)
	s.Body.Pos.Y = 2.5
	s.snapBodyToSurface(world)
	want := s.RestHeight
	if math.Abs(s.Body.Pos.Y-want) > 0.05 {
		t.Fatalf("snap Y=%.3f, want ~%.3f", s.Body.Pos.Y, want)
	}
}
