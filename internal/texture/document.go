package texture

import (
	"raytracer/internal/textlayout"
)

// Dynamic document texture ids (see WGSL TEX_DOCUMENT_*).
const (
	DocumentBase  = 56
	DocumentCount = 16
	DocumentTexW  = 512
	DocumentTexH  = 512
)

var documentAtlas = NewFixedSlotAtlas(DocumentBase, DocumentCount, DocumentTexW, DocumentTexH)

// IsDocument reports whether id is a runtime document texture slot.
func IsDocument(id int) bool { return documentAtlas.Contains(id) }

// ClearDocuments removes all document textures.
func ClearDocuments() { documentAtlas.Clear() }

// CommitDocuments replaces all document slots in one generation bump (used on scene load/reload).
func CommitDocuments(imgs map[int]*CaptureImage) { documentAtlas.Commit(imgs) }

// SetDocument stores an RGBA8 image for a document texture slot.
func SetDocument(id int, img *CaptureImage) { documentAtlas.Set(id, img) }

// DocumentGPUVersion returns the current document texture generation.
func DocumentGPUVersion() uint64 { return documentAtlas.Version() }

// PackDocumentsGPU returns document slots as u32 pixels for GPU upload.
func PackDocumentsGPU() (pixels []uint32, loaded bool) { return documentAtlas.PackGPU() }

// DocumentAtlas exposes the underlying slot atlas for tests or future reuse patterns.
func DocumentAtlas() *FixedSlotAtlas { return documentAtlas }

// BitmapFromLayout converts a textlayout bitmap into a capture image.
func BitmapFromLayout(b *textlayout.Bitmap) *CaptureImage {
	if b == nil {
		return nil
	}
	return &CaptureImage{Width: b.Width, Height: b.Height, RGBA: b.RGBA}
}
