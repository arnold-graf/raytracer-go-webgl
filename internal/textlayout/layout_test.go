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
	if got[0] != "one two" {
		t.Fatalf("expected collapsed spaces, got %q", got[0])
	}
}

func TestNormalizeLinesPreservesSpace(t *testing.T) {
	got := NormalizeLines([]string{"Duration:       1 hr", "Avg Dopamine:   0.521"})
	if len(got) != 2 {
		t.Fatalf("got %d lines: %v", len(got), got)
	}
	if got[0] != "Duration:       1 hr" {
		t.Fatalf("spaces collapsed: got %q", got[0])
	}
	if got[1] != "Avg Dopamine:   0.521" {
		t.Fatalf("spaces collapsed: got %q", got[1])
	}
}

func TestWrapTextCollapsesSpaces(t *testing.T) {
	face, err := LoadFace("", 16)
	if err != nil {
		t.Fatal(err)
	}
	got := WrapText("A       B", face, 400)
	if len(got) != 1 || got[0] != "A B" {
		t.Fatalf("WrapText collapsed spaces: %v", got)
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
