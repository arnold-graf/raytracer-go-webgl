package sceneio

import "testing"

func TestBookshelfInFrontOffice(t *testing.T) {
	s, err := Load("../../scenes/office-sunset/server-room-front-office.toml")
	if err != nil {
		t.Fatal(err)
	}
	var shelfBoxes int
	for _, b := range s.Boxes {
		mn, mx := b.WorldBounds()
		if mx.X > 2.5 && mn.X < 5.0 && mx.Z > 5.5 && mn.Z < 7.0 && mx.Y < 1.0 && mn.Y >= 0 {
			shelfBoxes++
		}
	}
	if shelfBoxes < 5 {
		t.Fatalf("expected bookshelf boxes near (2,8.5), got %d", shelfBoxes)
	}
}
