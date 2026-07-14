package texture

import (
	"sync"

	"raytracer/internal/vec"
)

// Dynamic capture texture ids (see also WGSL TEX_CAPTURE_* constants).
const (
	CaptureForward = 50
	CaptureLeft    = 51
	CaptureRight   = 52
	CaptureUp      = 53
	CaptureDown    = 54
	CaptureBackward = 55

	CaptureForwardName  = "capture_forward"
	CaptureLeftName     = "capture_left"
	CaptureRightName    = "capture_right"
	CaptureUpName       = "capture_up"
	CaptureDownName     = "capture_down"
	CaptureBackwardName = "capture_backward"

	captureSlotCount = 6
)

func init() {
	byName[CaptureForwardName] = CaptureForward
	byName[CaptureLeftName] = CaptureLeft
	byName[CaptureRightName] = CaptureRight
	byName[CaptureUpName] = CaptureUp
	byName[CaptureDownName] = CaptureDown
	byName[CaptureBackwardName] = CaptureBackward
}

var (
	captureMu      sync.RWMutex
	captures       = map[int]*CaptureImage{}
	captureVersion uint64
)

// CaptureIDs lists portal slots in shader upload order: forward, left, right, up, down, backward.
var CaptureIDs = [captureSlotCount]int{
	CaptureForward, CaptureLeft, CaptureRight, CaptureUp, CaptureDown, CaptureBackward,
}

// CaptureImage is a CPU-side RGBA8 image uploaded to the GPU when present.
type CaptureImage struct {
	Width, Height int
	RGBA          []byte
}

// PortalCapture holds the six square screenshots taken before a portal transition.
type PortalCapture struct {
	Width, Height int
	Images        [captureSlotCount][]byte // RGBA8, len = Width*Height*4 each
}

// IsCapture reports whether id is a runtime capture texture slot.
func IsCapture(id int) bool {
	return id >= CaptureForward && id <= CaptureBackward
}

// CommitCaptures stores a full portal capture set and bumps the GPU generation once.
func CommitCaptures(cap PortalCapture) {
	captureMu.Lock()
	defer captureMu.Unlock()
	w, h := cap.Width, cap.Height
	for i, id := range CaptureIDs {
		rgba := cap.Images[i]
		if !IsCapture(id) || w <= 0 || h <= 0 || len(rgba) != w*h*4 {
			delete(captures, id)
			continue
		}
		dup := make([]byte, len(rgba))
		copy(dup, rgba)
		captures[id] = &CaptureImage{Width: w, Height: h, RGBA: dup}
	}
	captureVersion++
}

// GetCapture returns the image for id, or nil.
func GetCapture(id int) *CaptureImage {
	captureMu.RLock()
	defer captureMu.RUnlock()
	return captures[id]
}

// ClearCaptures removes all runtime capture images.
func ClearCaptures() {
	captureMu.Lock()
	captures = map[int]*CaptureImage{}
	captureVersion++
	captureMu.Unlock()
}

// CaptureGPUVersion returns the current capture texture generation.
func CaptureGPUVersion() uint64 {
	captureMu.RLock()
	defer captureMu.RUnlock()
	return captureVersion
}

// EvalCapture samples a capture texture at UV (0..1). Returns tint when missing.
func EvalCapture(id int, u, v float64, tint vec.V) vec.V {
	img := GetCapture(id)
	if img == nil || len(img.RGBA) == 0 {
		return tint
	}
	u = clampCapture01(u)
	v = clampCapture01(v)
	x := int(u * float64(img.Width-1))
	y := int((1 - v) * float64(img.Height-1))
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	off := (y*img.Width + x) * 4
	if off+2 >= len(img.RGBA) {
		return tint
	}
	r := float64(img.RGBA[off]) / 255
	g := float64(img.RGBA[off+1]) / 255
	b := float64(img.RGBA[off+2]) / 255
	return vec.New(r*tint.X, g*tint.Y, b*tint.Z)
}

func clampCapture01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// PackCapturesGPU returns six RGBA8 images as u32 pixels (little-endian) for GPU upload.
func PackCapturesGPU() (w, h int, pixels []uint32, ok bool) {
	captureMu.RLock()
	defer captureMu.RUnlock()
	var ref *CaptureImage
	for _, id := range CaptureIDs {
		if captures[id] != nil {
			ref = captures[id]
			break
		}
	}
	if ref == nil {
		return 0, 0, nil, false
	}
	w, h = ref.Width, ref.Height
	n := w * h
	pixels = make([]uint32, n*captureSlotCount)
	for i, id := range CaptureIDs {
		img := captures[id]
		off := i * n
		if img == nil || img.Width != w || img.Height != h || len(img.RGBA) != n*4 {
			continue
		}
		for j := 0; j < n; j++ {
			b := j * 4
			pixels[off+j] = uint32(img.RGBA[b]) | uint32(img.RGBA[b+1])<<8 |
				uint32(img.RGBA[b+2])<<16 | 255<<24
		}
	}
	return w, h, pixels, true
}
