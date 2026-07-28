package scene

import (
	"math"

	"raytracer/internal/texture"
)

// StepMaterial is the acoustic category of the surface a player is standing on,
// used to pick a footstep sound. It deliberately collapses the many visual
// textures into the few that sound distinct.
type StepMaterial int

const (
	StepGrass StepMaterial = iota // soft ground: terrain, grass, dirt
	StepHard                      // stone, marble, cement, brick, tile
	StepWood                      // wooden boards (hollow, creaky)
	StepSnow                      // packed powder (muffled, no bright transient)
)

// stepMaterialForTexture maps a procedural texture id to its acoustic category.
func stepMaterialForTexture(tex int) StepMaterial {
	switch tex {
	case texture.Wood:
		return StepWood
	case texture.Grass, texture.Dirt:
		return StepGrass
	case texture.Snow:
		return StepSnow
	default:
		// marble, stone, cement, brick, wallpaper, untextured → hard.
		return StepHard
	}
}

// footMaterial reports the acoustic category of the terrain surface at altitude
// h: snow-covered high ground sounds like snow, everything else like grass. It
// mirrors AlbedoAt's altitude-based snow blend (slope is ~flat where a player
// can stand, so the rock layer is not considered underfoot).
func (t *Terrain) footMaterial(h float64) StepMaterial {
	if t.SnowHi > t.SnowLo && smoothstep(t.SnowLo, t.SnowHi, h) > 0.5 {
		return StepSnow
	}
	return StepGrass
}

// StepMaterialAt reports the acoustic material of the highest walkable surface
// under (x, z) whose top is at or below headY. It mirrors GroundHeight's
// selection so the sound always matches the surface the player is actually
// standing on: a box floor (by its texture) when one is underfoot, otherwise the
// terrain (treated as grass). When nothing is found it defaults to grass.
func (s *Scene) StepMaterialAt(x, z, headY float64) StepMaterial {
	bestY := math.Inf(-1)
	mat := StepGrass

	for i := range s.Terrains {
		t := &s.Terrains[i]
		if x >= t.OriginX && x <= t.OriginX+t.SizeX && z >= t.OriginZ && z <= t.OriginZ+t.SizeZ {
			if h := t.Height(x, z); h > bestY {
				bestY, mat = h, t.footMaterial(h)
			}
		}
	}

	for i := range s.Boxes {
		mn, mx := s.Boxes[i].WorldBounds()
		if mx.Y <= headY && x >= mn.X && x <= mx.X && z >= mn.Z && z <= mx.Z {
			if mx.Y > bestY {
				bestY = mx.Y
				mat = stepMaterialForTexture(s.Boxes[i].Tex)
			}
		}
	}

	for i := range s.Cones {
		if h, ok := s.Cones[i].capGroundHeight(x, z, headY); ok && h > bestY {
			bestY = h
			mat = stepMaterialForTexture(s.Cones[i].Tex)
		}
	}

	for i := range s.Cylinders {
		if h, ok := s.Cylinders[i].capGroundHeight(x, z, headY); ok && h > bestY {
			bestY = h
			mat = stepMaterialForTexture(s.Cylinders[i].Tex)
		}
	}

	return mat
}
