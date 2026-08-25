package texture_test

import (
	"testing"

	"raytracer/internal/texture"
)

func TestParseTiles(t *testing.T) {
	id, w, h, err := texture.Parse("tiles(0.3, 0.1)")
	if err != nil {
		t.Fatal(err)
	}
	if id != texture.Tiles || w != 0.3 || h != 0.1 {
		t.Fatalf("got id=%d w=%v h=%v", id, w, h)
	}
}

func TestParseTilesDefaultSize(t *testing.T) {
	id, w, h, err := texture.Parse("tiles")
	if err != nil {
		t.Fatal(err)
	}
	if id != texture.Tiles || w != 1 || h != 1 {
		t.Fatalf("got id=%d w=%v h=%v", id, w, h)
	}
	id, w, h, err = texture.Parse("tiles()")
	if err != nil {
		t.Fatal(err)
	}
	if id != texture.Tiles || w != 1 || h != 1 {
		t.Fatalf("empty parens: id=%d w=%v h=%v", id, w, h)
	}
}

func TestParseSimpleName(t *testing.T) {
	id, w, h, err := texture.Parse("wood")
	if err != nil {
		t.Fatal(err)
	}
	if id != texture.Wood || w != 1 || h != 1 {
		t.Fatalf("got id=%d w=%v h=%v", id, w, h)
	}
}
