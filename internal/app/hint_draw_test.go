package app

import (
	"strings"
	"testing"

	"raytracer/internal/sceneio"
)

func TestHintFontFaceLoads(t *testing.T) {
	g := &Game{}
	face, err := g.hintFontFace()
	if err != nil {
		t.Fatal(err)
	}
	if face == nil {
		t.Fatal("nil face")
	}
	if !strings.HasSuffix(g.hintFontFilePath(), "M42.ttf") {
		t.Fatalf("font path = %q", g.hintFontFilePath())
	}
}

func (g *Game) hintFontFilePath() string {
	path, err := sceneio.ResolveFontPath(hintFontFile)
	if err != nil {
		return ""
	}
	return path
}
