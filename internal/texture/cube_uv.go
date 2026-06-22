package texture

import "raytracer/internal/vec"

// Cube interior face bounds (see scenes/objects/cube.toml). Shared across all
// five walls so adjacent faces align; must stay in sync with capture_room_uv in
// internal/webgpu/shaders/trace.wgsl.
const (
	CubeX0 = -1.0
	CubeX1 = 4.0
	CubeY0 = -1.0
	CubeY1 = 4.0
	CubeZ0 = -1.0
	CubeZ1 = 4.0
)

// CubeRoomUV maps a hit point on a cube interior face to texture coordinates
// (u,v) in [0,1], with v=0 at the top of the capture image.
func CubeRoomUV(p, n vec.V) (u, v float64) {
	ax, ay, az := abs3(n.X), abs3(n.Y), abs3(n.Z)
	switch {
	case az >= ax && az >= ay:
		u = (p.X - CubeX0) / (CubeX1 - CubeX0)
		v = (p.Y - CubeY0) / (CubeY1 - CubeY0)
	case ax >= ay:
		u = (p.Z - CubeZ0) / (CubeZ1 - CubeZ0)
		v = (p.Y - CubeY0) / (CubeY1 - CubeY0)
		if n.X > 0 {
			u = 1 - u
		}
	default:
		u = (p.X - CubeX0) / (CubeX1 - CubeX0)
		vz := (p.Z - CubeZ0) / (CubeZ1 - CubeZ0)
		u = clampCapture01(u)
		if n.Y > 0 {
			// Floor (+Y): v increases with +Z to match right wall and capture_down.
			return u, clampCapture01(vz)
		}
		v = vz
	}
	u = clampCapture01(u)
	v = clampCapture01(1 - v)
	return u, v
}

func abs3(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
