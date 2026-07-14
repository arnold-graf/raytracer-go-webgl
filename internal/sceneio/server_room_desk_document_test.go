package sceneio

import (
	"path/filepath"
	"testing"

	"raytracer/internal/texture"
)

func TestServerRoomDeskDocumentTexture(t *testing.T) {
	path := filepath.Join("..", "..", "scenes", "office-sunset", "objects", "server-room-desk.toml")
	sc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.DocumentSpecs) != 1 {
		t.Fatalf("DocumentSpecs = %d, want 1", len(sc.DocumentSpecs))
	}
	texID := sc.DocumentSpecs[0].TexID
	if !texture.IsDocument(texID) {
		t.Fatalf("tex id %d is not a document slot", texID)
	}
	if texture.IsCapture(texID) {
		t.Fatalf("tex id %d must not alias a capture slot", texID)
	}
	pixels, loaded := texture.PackDocumentsGPU()
	if !loaded {
		t.Fatal("expected document GPU pack after load")
	}
	slot := texID - texture.DocumentBase
	slotPixels := texture.DocumentTexW * texture.DocumentTexH
	off := slot * slotPixels
	if off+slotPixels > len(pixels) {
		t.Fatalf("slot offset out of range for tex id %d", texID)
	}
	nonEmpty := false
	for _, px := range pixels[off : off+slotPixels] {
		if px != 0 {
			nonEmpty = true
			break
		}
	}
	if !nonEmpty {
		t.Fatal("expected non-empty rasterized document pixels")
	}
}
