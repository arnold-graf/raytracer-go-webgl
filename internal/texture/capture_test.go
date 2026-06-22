package texture_test

import (
	"math"
	"testing"

	"raytracer/internal/texture"
	"raytracer/internal/vec"
)

func TestCommitCapturesAndPackGPU(t *testing.T) {
	texture.ClearCaptures()
	ver0 := texture.CaptureGPUVersion()

	px := make([]byte, 2*2*4)
	px[0], px[1], px[2] = 10, 20, 30
	cap := texture.PortalCapture{
		Width: 2, Height: 2,
		Images: [5][]byte{px, px, px, px, px},
	}
	texture.CommitCaptures(cap)
	if texture.CaptureGPUVersion() != ver0+1 {
		t.Fatalf("expected one version bump")
	}

	w, h, packed, ok := texture.PackCapturesGPU()
	if !ok || w != 2 || h != 2 || len(packed) != 2*2*5 {
		t.Fatalf("pack: ok=%v w=%d h=%d len=%d", ok, w, h, len(packed))
	}
	if packed[0]&0xff != 10 || (packed[0]>>8)&0xff != 20 {
		t.Fatalf("first pixel = %08x, want RGB 10,20,30", packed[0])
	}
}

func TestCubeRoomUVFaceCenters(t *testing.T) {
	center := vec.New(1.5, 1.5, 1.5)
	const eps = 1e-9

	tests := []struct {
		name string
		n    vec.V
		wantU, wantV float64
	}{
		{"front +Z", vec.New(0, 0, 1), 0.5, 0.5},
		{"left +X", vec.New(1, 0, 0), 0.5, 0.5},
		{"right -X", vec.New(-1, 0, 0), 0.5, 0.5},
		{"floor +Y", vec.New(0, 1, 0), 0.5, 0.5},
		{"ceiling -Y", vec.New(0, -1, 0), 0.5, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, v := texture.CubeRoomUV(center, tc.n)
			if math.Abs(u-tc.wantU) > eps || math.Abs(v-tc.wantV) > eps {
				t.Fatalf("uv = (%v,%v), want (%v,%v)", u, v, tc.wantU, tc.wantV)
			}
		})
	}
}

func TestCubeRoomUVSharedEdges(t *testing.T) {
	// Front-bottom edge: front wall and floor should agree on u at the corner.
	frontBottom := vec.New(texture.CubeX0, texture.CubeY0, texture.CubeZ0)
	uFront, _ := texture.CubeRoomUV(frontBottom, vec.New(0, 0, 1))
	uFloor, _ := texture.CubeRoomUV(frontBottom, vec.New(0, 1, 0))
	if math.Abs(uFront-uFloor) > 1e-9 {
		t.Fatalf("front u=%v floor u=%v at shared corner", uFront, uFloor)
	}
}

func TestCubeRoomUVFloorRightWallEdge(t *testing.T) {
	// Front (−Z) on the floor should match the right wall (−X) at the shared edge.
	pt := vec.New(texture.CubeX1, 0.25, texture.CubeZ0)
	_, vFloor := texture.CubeRoomUV(pt, vec.New(0, 1, 0))
	uRight, _ := texture.CubeRoomUV(pt, vec.New(-1, 0, 0))
	if math.Abs(vFloor-uRight) > 1e-9 {
		t.Fatalf("floor v=%v right u=%v at shared corner", vFloor, uRight)
	}
}

func TestCubeRoomUVLeftWallForwardCorner(t *testing.T) {
	// Forward (−Z) on the left wall is u=1 (viewer right when facing the wall).
	pt := vec.New(texture.CubeX0, 1.5, texture.CubeZ0)
	u, _ := texture.CubeRoomUV(pt, vec.New(1, 0, 0))
	if math.Abs(u-1.0) > 1e-9 {
		t.Fatalf("forward corner u=%v, want 1", u)
	}
}
