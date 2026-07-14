package sceneio

import (
	"testing"
)

func TestLoadScreen(t *testing.T) {
	const src = `
[[screen]]
id = "terminal"
pos_x = 0.0
pos_y = 1.0
pos_z = 0.5
headline = "Hello"
paragraphs = ["Line one."]
font_size_px = 16
material = "emit"
albedo = [0.1, 0.1, 0.2]
font_color = [0.8, 1.0, 0.9]
`
	got, err := Decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeDocuments(got); err != nil {
		t.Fatal(err)
	}
	if len(got.ScreenSpecs) != 1 {
		t.Fatalf("ScreenSpecs = %d", len(got.ScreenSpecs))
	}
	ss := got.ScreenSpecs[0]
	if ss.ID != "terminal" || ss.Headline != "Hello" {
		t.Fatalf("spec = %+v", ss)
	}
	if ss.TexID == 0 {
		t.Fatal("expected tex id assigned")
	}
	if got.ScreenSpecs[0].Interact == nil || got.ScreenSpecs[0].Interact.Handler != "screen" {
		t.Fatalf("interact = %+v", got.ScreenSpecs[0].Interact)
	}
	if got.ScreenSpecs[0].Interact.Hint != "computer screen" {
		t.Fatalf("hint = %q", got.ScreenSpecs[0].Interact.Hint)
	}
}
