package sceneio

import (
	"testing"
)

func TestLoadDocument(t *testing.T) {
	const src = `
[[document]]
id = "note"
pos_x = 1.0
pos_y = 0.8
pos_z = 2.0
headline = "Hello"
paragraphs = ["Line one.", "Line two."]
font_size_px = 16
`
	got, err := Decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeDocuments(got); err != nil {
		t.Fatal(err)
	}
	if len(got.DocumentSpecs) != 1 {
		t.Fatalf("DocumentSpecs = %d", len(got.DocumentSpecs))
	}
	ds := got.DocumentSpecs[0]
	if ds.ID != "note" || ds.Headline != "Hello" {
		t.Fatalf("spec = %+v", ds)
	}
	if ds.TexID == 0 {
		t.Fatal("expected tex id assigned")
	}
	if got.DocumentSpecs[0].Interact == nil || got.DocumentSpecs[0].Interact.Handler != "document" {
		t.Fatalf("document interact = %+v", got.DocumentSpecs[0].Interact)
	}
}

func TestLoadDocumentCustomHint(t *testing.T) {
	const src = `
[[document]]
pos_x = 0.0
pos_y = 0.0
pos_z = 0.0
hint = "read the memo"
`
	got, err := Decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got.DocumentSpecs[0].Interact.Hint != "read the memo" {
		t.Fatalf("hint = %q", got.DocumentSpecs[0].Interact.Hint)
	}
}
