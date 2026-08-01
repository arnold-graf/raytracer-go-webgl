package webgpu

import (
	"unsafe"

	"raytracer/internal/scene"
)

const (
	maxFlameParticles  = scene.FlameParticlesPerCampfire * maxCampfires
	flameParticleStride = 32
)

// GPUFlameParticle mirrors FlameParticle in types.wesl (std430, 32-byte stride).
type GPUFlameParticle struct {
	Pos    [4]float32 // xyz, radius
	Color  [4]float32 // rgb, _
}

// PackFlameParticles converts simulated flame particles for the GPU buffer.
func PackFlameParticles(parts []scene.FlameParticle) []GPUFlameParticle {
	if len(parts) == 0 {
		return nil
	}
	if len(parts) > maxFlameParticles {
		parts = parts[:maxFlameParticles]
	}
	out := make([]GPUFlameParticle, len(parts))
	for i, p := range parts {
		out[i] = GPUFlameParticle{
			Pos:   [4]float32{f(p.Pos.X), f(p.Pos.Y), f(p.Pos.Z), f(p.Radius)},
			Color: [4]float32{f(p.Color.X), f(p.Color.Y), f(p.Color.Z), 0},
		}
	}
	return out
}

func flameBytes(particles []GPUFlameParticle) []byte {
	if len(particles) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&particles[0])), len(particles)*flameParticleStride)
}
