package sceneio

import (
	"path/filepath"
	"strings"
	"testing"
)

// LoadDeps must report included sub-scenes so the hot-reload watcher can detect
// edits to them (e.g. building.toml), not just the top-level file.
func TestLoadDepsIncludesSubScenes(t *testing.T) {
	_, deps, err := LoadDeps(repoFile("scenes/indoor-outdoor.toml"))
	if err != nil {
		t.Fatalf("LoadDeps: %v", err)
	}
	var hasParent, hasBuilding bool
	for _, d := range deps {
		switch filepath.Base(d) {
		case "indoor-outdoor.toml":
			hasParent = true
		case "building.toml":
			hasBuilding = true
		}
		if !filepath.IsAbs(d) {
			t.Errorf("dep not absolute: %q", d)
		}
	}
	if !hasParent || !hasBuilding {
		t.Fatalf("deps missing parent/include: %s", strings.Join(deps, ", "))
	}
}

func TestBuildingIncludeLoads(t *testing.T) {
	s, err := Load(repoFile("scenes/indoor-outdoor.toml"))
	if err != nil {
		t.Fatalf("load indoor-outdoor: %v", err)
	}
	if len(s.Boxes) < 15 {
		t.Fatalf("expected building boxes from include, got %d", len(s.Boxes))
	}
	var glass, roof int
	for i := range s.Boxes {
		b := &s.Boxes[i]
		if b.Mat == 4 { // MatGlass
			glass++
		}
		if b.Xform != nil && b.Tex != 0 {
			roof++ // slanted cement roof has a transform
		}
	}
	if glass != 1 {
		t.Fatalf("glass windows = %d, want 1", glass)
	}
	if roof < 1 {
		t.Fatalf("expected at least one transformed roof panel")
	}
}
