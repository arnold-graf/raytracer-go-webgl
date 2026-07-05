package texture

import "sync"

// FixedSlotAtlas stores same-size RGBA images in a contiguous GPU slot range.
// Document textures use one atlas; future bitmap props (signs, screens) can use another.
type FixedSlotAtlas struct {
	mu      sync.RWMutex
	base    int
	count   int
	width   int
	height  int
	images  map[int]*CaptureImage
	version uint64
}

// NewFixedSlotAtlas returns an atlas for ids [base, base+count).
func NewFixedSlotAtlas(base, count, width, height int) *FixedSlotAtlas {
	return &FixedSlotAtlas{
		base:   base,
		count:  count,
		width:  width,
		height: height,
		images: map[int]*CaptureImage{},
	}
}

// Contains reports whether id falls in this atlas.
func (a *FixedSlotAtlas) Contains(id int) bool {
	return id >= a.base && id < a.base+a.count
}

// Clear removes all images and bumps the GPU generation.
func (a *FixedSlotAtlas) Clear() {
	a.mu.Lock()
	a.images = map[int]*CaptureImage{}
	a.version++
	a.mu.Unlock()
}

// Commit replaces all slots in one generation bump.
func (a *FixedSlotAtlas) Commit(imgs map[int]*CaptureImage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.images = map[int]*CaptureImage{}
	for id, img := range imgs {
		if !a.Contains(id) || img == nil {
			continue
		}
		dup := make([]byte, len(img.RGBA))
		copy(dup, img.RGBA)
		a.images[id] = &CaptureImage{Width: img.Width, Height: img.Height, RGBA: dup}
	}
	a.version++
}

// Set stores one slot image.
func (a *FixedSlotAtlas) Set(id int, img *CaptureImage) {
	if !a.Contains(id) || img == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	dup := make([]byte, len(img.RGBA))
	copy(dup, img.RGBA)
	a.images[id] = &CaptureImage{Width: img.Width, Height: img.Height, RGBA: dup}
	a.version++
}

// Version returns the current GPU generation.
func (a *FixedSlotAtlas) Version() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.version
}

// PackGPU returns slot pixels as u32 values for GPU upload. Missing slots stay zero.
func (a *FixedSlotAtlas) PackGPU() (pixels []uint32, loaded bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	slotPixels := a.width * a.height
	pixels = make([]uint32, slotPixels*a.count)
	loaded = false
	for slot := 0; slot < a.count; slot++ {
		id := a.base + slot
		img := a.images[id]
		if img == nil {
			continue
		}
		off := slot * slotPixels
		n := slotPixels
		if img.Width != a.width || img.Height != a.height || len(img.RGBA) != n*4 {
			continue
		}
		for j := 0; j < n; j++ {
			b := j * 4
			pixels[off+j] = uint32(img.RGBA[b]) | uint32(img.RGBA[b+1])<<8 |
				uint32(img.RGBA[b+2])<<16 | 255<<24
		}
		loaded = true
	}
	return pixels, loaded
}
