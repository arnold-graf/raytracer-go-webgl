package sceneio_test

import (
	"path/filepath"
	"testing"

	"raytracer/internal/scene"
	"raytracer/internal/sceneio"
)

func TestOfficeSunsetSkyscraperInstancing(t *testing.T) {
	root := filepath.Join("..", "..")
	scenePath := filepath.Join(root, "scenes", "office-sunset", "index.toml")
	sc, err := sceneio.Load(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	sc.FinalizeInstancing()
	if !sc.HasInstancing() {
		t.Fatal("expected instancing on office-sunset")
	}
	cat := sc.Instancing()
	var skyTemplates, skyPlacements int
	for _, pl := range cat.Placements {
		tmpl := cat.Templates[pl.TemplateIndex]
		if filepath.Base(tmpl.Source) != "skyscrapers-distance.toml" {
			continue
		}
		skyPlacements++
		wmin, wmax, ok := scene.TemplateWorldBounds(tmpl.Scene, pl.Xform)
		if !ok {
			t.Fatalf("skyscraper placement %d has no bounds", skyPlacements)
		}
		if skyPlacements == 1 && pl.Xform != nil {
			t.Fatalf("first skyscraper placement should use nil identity xform, got %+v", pl.Xform)
		}
		t.Logf("sky placement xf=%+v bounds min=%+v max=%+v", pl.Xform, wmin, wmax)
	}
	if skyPlacements != 2 {
		t.Fatalf("skyscraper placements = %d, want 2", skyPlacements)
	}
	for _, tmpl := range cat.Templates {
		if filepath.Base(tmpl.Source) == "skyscrapers-distance.toml" {
			skyTemplates++
			t.Logf("sky template boxes=%d cylinders=%d", len(tmpl.Scene.Boxes), len(tmpl.Scene.Cylinders))
		}
	}
	if skyTemplates != 1 {
		t.Fatalf("skyscraper templates = %d, want 1", skyTemplates)
	}

}
