package character

import (
	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

// FootWorld answers terrain queries for foot placement and grounding.
type FootWorld interface {
	GroundHeight(x, z, headY float64) float64
	GroundNormal(x, z, headY float64) vec.V
}

// SurfaceHit is a ray/sphere cast result for spider surface probing.
type SurfaceHit struct {
	Point  vec.V
	Normal vec.V
	Hit    bool
	Dist   float64
}

// SpiderWorld extends FootWorld with directional surface casts for wall-walking
// and procedural foot placement (PhilS94 spider architecture).
type SpiderWorld interface {
	FootWorld
	CastRay(origin, dir vec.V, maxDist, headY float64) SurfaceHit
}

// CastFromFootWorld wraps a FootWorld with horizontal-down ray fallback.
func CastFromFootWorld(w FootWorld, origin, dir vec.V, maxDist, headY float64) SurfaceHit {
	if sw, ok := w.(SpiderWorld); ok {
		return sw.CastRay(origin, dir, maxDist, headY)
	}
	if w == nil {
		return SurfaceHit{}
	}
	if dir.LenSq() < 1e-18 {
		return SurfaceHit{}
	}
	dir = dir.Normalize()
	// Fallback: march along ray sampling GroundHeight (flat terrain / tests).
	const steps = 12
	step := maxDist / float64(steps)
	for i := 1; i <= steps; i++ {
		t := step * float64(i)
		p := origin.Add(dir.Scale(t))
		gy := w.GroundHeight(p.X, p.Z, headY)
		if p.Y <= gy+0.02 {
			n := w.GroundNormal(p.X, p.Z, headY)
			return SurfaceHit{Point: vec.V{X: p.X, Y: gy, Z: p.Z}, Normal: n, Hit: true, Dist: t}
		}
	}
	return SurfaceHit{}
}

// SceneSpiderWorld adapts a scene for spider casts (static geometry only).
type SceneSpiderWorld struct {
	Sc *scene.Scene
}

func (w SceneSpiderWorld) GroundHeight(x, z, headY float64) float64 {
	if w.Sc == nil {
		return 0
	}
	return w.Sc.GroundHeightStatic(x, z, headY)
}

func (w SceneSpiderWorld) GroundNormal(x, z, headY float64) vec.V {
	if w.Sc == nil {
		return vec.V{Y: 1}
	}
	return w.Sc.GroundNormalStatic(x, z, headY)
}

func (w SceneSpiderWorld) CastRay(origin, dir vec.V, maxDist, headY float64) SurfaceHit {
	if w.Sc == nil || dir.LenSq() < 1e-18 {
		return SurfaceHit{}
	}
	hit := w.Sc.CastRayStatic(vec.Ray{Origin: origin, Dir: dir.Normalize()}, maxDist)
	if !hit.Hit {
		return SurfaceHit{}
	}
	return SurfaceHit{Point: hit.Point, Normal: hit.Normal, Hit: true, Dist: hit.Dist}
}

var _ SpiderWorld = SceneSpiderWorld{}
