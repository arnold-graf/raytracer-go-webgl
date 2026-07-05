package textlayout

import (
	"strings"
	"testing"
)

func TestWrapText(t *testing.T) {
	face, err := LoadFace("", 16)
	if err != nil {
		t.Fatal(err)
	}
	lines := WrapText("hello world from the ray tracer", face, 80)
	if len(lines) < 2 {
		t.Fatalf("expected wrap into multiple lines, got %v", lines)
	}
}

func TestNormalizeParagraphs(t *testing.T) {
	got := NormalizeParagraphs([]string{"one two", "three\n\nfour"})
	if len(got) != 3 {
		t.Fatalf("got %d paragraphs: %v", len(got), got)
	}
	if strings.Contains(got[0], "\n") {
		t.Fatalf("expected collapsed space, got %q", got[0])
	}
}

func TestRasterizePaper(t *testing.T) {
	style := PaperStyle(512, 512, 18)
	bmp, err := Rasterize("Title", []string{"Body text for the page."}, "", style)
	if err != nil {
		t.Fatal(err)
	}
	if bmp.Width != 512 || bmp.Height != 512 {
		t.Fatalf("size = %dx%d", bmp.Width, bmp.Height)
	}
	if len(bmp.RGBA) != 512*512*4 {
		t.Fatalf("rgba len = %d", len(bmp.RGBA))
	}
}
