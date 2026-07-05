package texture

import (
	"testing"
)

func TestRasterizeDocument(t *testing.T) {
	img, err := RasterizeDocument("Title", []string{"Body text for the page."}, "", 18)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != DocumentTexW || img.Height != DocumentTexH {
		t.Fatalf("size = %dx%d", img.Width, img.Height)
	}
	if len(img.RGBA) != DocumentTexW*DocumentTexH*4 {
		t.Fatalf("rgba len = %d", len(img.RGBA))
	}
}

func TestNormalizeParagraphs(t *testing.T) {
	got := NormalizeParagraphs([]string{"one two", "three\n\nfour"})
	if len(got) != 3 {
		t.Fatalf("got %d paragraphs: %v", len(got), got)
	}
}

func TestFixedSlotAtlasCommit(t *testing.T) {
	atlas := NewFixedSlotAtlas(100, 2, 4, 4)
	imgs := map[int]*CaptureImage{
		100: {Width: 4, Height: 4, RGBA: make([]byte, 64)},
	}
	atlas.Commit(imgs)
	px, ok := atlas.PackGPU()
	if !ok || len(px) != 2*4*4 {
		t.Fatalf("pack = %d ok=%v", len(px), ok)
	}
	if atlas.Version() == 0 {
		t.Fatal("expected version bump")
	}
}
