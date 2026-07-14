package sceneio

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFontPath(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveFontPath("M42.ttf")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "assets", "M42.ttf")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got, err = resolveFontPath("assets/PixelOperator.ttf")
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(root, "assets", "PixelOperator.ttf")
	if got != want {
		t.Fatalf("assets/ prefix: got %q want %q", got, want)
	}

	got, err = resolveFontPath("../../../assets/M42.ttf")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "assets", "M42.ttf") {
		t.Fatalf("legacy path: got %q", got)
	}

	got, err = resolveFontPath("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join("assets", defaultFontFile)) {
		t.Fatalf("default font: got %q", got)
	}
}
