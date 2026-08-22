package joltphys

import (
	"math"

	"github.com/bbitechnologies/jolt-go/jolt"

	"raytracer/internal/scene"
	"raytracer/internal/vec"
)

func quatToMat3(q jolt.Quat) scene.Mat3 {
	x, y, z, w := float64(q.X), float64(q.Y), float64(q.Z), float64(q.W)
	return scene.Mat3{
		1 - 2*(y*y+z*z), 2*(x*y - z*w), 2*(x*z + y*w),
		2*(x*y + z*w), 1 - 2*(x*x+z*z), 2*(y*z - x*w),
		2*(x*z - y*w), 2*(y*z + x*w), 1 - 2*(x*x+y*y),
	}
}

func mat3ToQuat(m scene.Mat3) jolt.Quat {
	// Shepperd's method
	tr := m[0] + m[4] + m[8]
	switch {
	case tr > 0:
		s := math.Sqrt(tr+1) * 2
		return jolt.Quat{
			X: float32((m[7] - m[5]) / s),
			Y: float32((m[2] - m[6]) / s),
			Z: float32((m[3] - m[1]) / s),
			W: float32(0.25 * s),
		}
	case m[0] > m[4] && m[0] > m[8]:
		s := math.Sqrt(1+m[0]-m[4]-m[8]) * 2
		return jolt.Quat{
			X: float32(0.25 * s),
			Y: float32((m[1] + m[3]) / s),
			Z: float32((m[2] + m[6]) / s),
			W: float32((m[7] - m[5]) / s),
		}
	case m[4] > m[8]:
		s := math.Sqrt(1+m[4]-m[0]-m[8]) * 2
		return jolt.Quat{
			X: float32((m[1] + m[3]) / s),
			Y: float32(0.25 * s),
			Z: float32((m[5] + m[7]) / s),
			W: float32((m[2] - m[6]) / s),
		}
	default:
		s := math.Sqrt(1+m[8]-m[0]-m[4]) * 2
		return jolt.Quat{
			X: float32((m[2] + m[6]) / s),
			Y: float32((m[5] + m[7]) / s),
			Z: float32(0.25 * s),
			W: float32((m[3] - m[1]) / s),
		}
	}
}

func transformFromJolt(pos jolt.Vec3, rot jolt.Quat) *scene.Transform {
	fwd := quatToMat3(rot)
	return scene.RigidFromBasis(vec.V{
		X: float64(pos.X),
		Y: float64(pos.Y),
		Z: float64(pos.Z),
	}, fwd)
}

func joltPoseFromTransform(xf *scene.Transform) (jolt.Vec3, jolt.Quat) {
	if xf == nil {
		return jolt.Vec3{}, jolt.QuatIdentity()
	}
	pos := xf.Translation()
	return jolt.Vec3{
		X: float32(pos.X),
		Y: float32(pos.Y),
		Z: float32(pos.Z),
	}, mat3ToQuat(xf.Fwd())
}
