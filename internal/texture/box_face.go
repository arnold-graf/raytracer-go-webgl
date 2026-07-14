package texture

import "raytracer/internal/vec"

// Box face indices for Box.FaceTex (+X, -X, +Y, -Y, +Z, -Z).
const (
	BoxFacePosX = 0 // texture_right
	BoxFaceNegX = 1 // texture_left
	BoxFacePosY = 2 // texture_top
	BoxFaceNegY = 3 // texture_bottom
	BoxFacePosZ = 4 // texture_front
	BoxFaceNegZ = 5 // texture_back
)

// MaxCaptureDim is the largest square side stored in the GPU capture atlas.
const MaxCaptureDim = 512

// BoxFaceIndex returns the dominant face index (0..5) for normal n, or -1.
func BoxFaceIndex(n vec.V) int {
	ax, ay, az := abs3(n.X), abs3(n.Y), abs3(n.Z)
	switch {
	case az >= ax && az >= ay:
		if n.Z >= 0 {
			return BoxFacePosZ
		}
		return BoxFaceNegZ
	case ax >= ay:
		if n.X >= 0 {
			return BoxFacePosX
		}
		return BoxFaceNegX
	default:
		if n.Y >= 0 {
			return BoxFacePosY
		}
		return BoxFaceNegY
	}
}

// BoxFaceUV maps a hit on one box face to texture coordinates in [0,1].
// v=0 is the top of the image (high Y on vertical faces, low Z on +Y floor face).
// Returns u,v < 0 when the face is degenerate.
func BoxFaceUV(p, n, bmin, bmax vec.V) (u, v float64) {
	face := BoxFaceIndex(n)
	dx := bmax.X - bmin.X
	dy := bmax.Y - bmin.Y
	dz := bmax.Z - bmin.Z
	if dx <= 0 || dy <= 0 || dz <= 0 {
		return -1, -1
	}
	switch face {
	case BoxFacePosZ:
		u = (p.X - bmin.X) / dx
		v = 1 - (p.Y-bmin.Y)/dy
	case BoxFaceNegZ:
		u = 1 - (p.X-bmin.X)/dx
		v = 1 - (p.Y-bmin.Y)/dy
	case BoxFacePosX:
		u = 1 - (p.Z-bmin.Z)/dz
		v = 1 - (p.Y-bmin.Y)/dy
	case BoxFaceNegX:
		u = (p.Z - bmin.Z) / dz
		v = 1 - (p.Y-bmin.Y)/dy
	case BoxFacePosY:
		u = 1 - (p.X-bmin.X)/dx
		v = 1 - (p.Z-bmin.Z)/dz
	case BoxFaceNegY:
		u = (p.X - bmin.X) / dx
		v = 1 - (p.Z-bmin.Z)/dz
	default:
		return -1, -1
	}
	return clampCapture01(u), clampCapture01(v)
}

func abs3(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
