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
		Images: [6][]byte{px, px, px, px, px, px},
	}
	texture.CommitCaptures(cap)
	if texture.CaptureGPUVersion() != ver0+1 {
		t.Fatalf("expected one version bump")
	}

	w, h, packed, ok := texture.PackCapturesGPU()
	if !ok || w != 2 || h != 2 || len(packed) != 2*2*6 {
		t.Fatalf("pack: ok=%v w=%d h=%d len=%d", ok, w, h, len(packed))
	}
	if packed[0]&0xff != 10 || (packed[0]>>8)&0xff != 20 {
		t.Fatalf("first pixel = %08x, want RGB 10,20,30", packed[0])
	}
}

func TestCaptureBackwardName(t *testing.T) {
	id, ok := texture.ID(texture.CaptureBackwardName)
	if !ok || id != texture.CaptureBackward {
		t.Fatalf("id=%d ok=%v", id, ok)
	}
}

func TestBoxFaceUVCenter(t *testing.T) {
	bmin := vec.New(-1, 0, -1)
	bmax := vec.New(4, 5, 4)
	center := vec.New(1.5, 2.5, 1.5)
	const eps = 1e-9

	tests := []struct {
		name         string
		n            vec.V
		wantU, wantV float64
	}{
		{"front +Z", vec.New(0, 0, 1), 0.5, 0.5},
		{"back -Z", vec.New(0, 0, -1), 0.5, 0.5},
		{"right +X", vec.New(1, 0, 0), 0.5, 0.5},
		{"left -X", vec.New(-1, 0, 0), 0.5, 0.5},
		{"top +Y", vec.New(0, 1, 0), 0.5, 0.5},
		{"bottom -Y", vec.New(0, -1, 0), 0.5, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, v := texture.BoxFaceUV(center, tc.n, bmin, bmax)
			if math.Abs(u-tc.wantU) > eps || math.Abs(v-tc.wantV) > eps {
				t.Fatalf("uv = (%v,%v), want (%v,%v)", u, v, tc.wantU, tc.wantV)
			}
		})
	}
}

func TestBoxFaceUVFloorCorner(t *testing.T) {
	// Cube floor slab: interior +Y face uses texture_top (capture_down).
	bmin := vec.New(-1, 0, -1)
	bmax := vec.New(4, 0.25, 4)
	pt := vec.New(-1, 0.25, -1) // min X, max Y, min Z corner
	u, v := texture.BoxFaceUV(pt, vec.New(0, 1, 0), bmin, bmax)
	if math.Abs(u-1) > 1e-9 || math.Abs(v-1) > 1e-9 {
		t.Fatalf("floor corner uv = (%v,%v), want (1,1)", u, v)
	}
	pt = vec.New(4, 0.25, 4)
	u, v = texture.BoxFaceUV(pt, vec.New(0, 1, 0), bmin, bmax)
	if math.Abs(u) > 1e-9 || math.Abs(v) > 1e-9 {
		t.Fatalf("floor opposite corner uv = (%v,%v), want (0,0)", u, v)
	}
}

func TestBoxFaceUVScalesWithBounds(t *testing.T) {
	bmin := vec.New(0, 0, 0)
	bmax := vec.New(10, 4, 8)
	pt := vec.New(10, 2, 4)
	u, v := texture.BoxFaceUV(pt, vec.New(1, 0, 0), bmin, bmax)
	if math.Abs(u-0.5) > 1e-9 || math.Abs(v-0.5) > 1e-9 {
		t.Fatalf("uv = (%v,%v), want center of +X face", u, v)
	}
}
