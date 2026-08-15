package shaders

import (
	"sort"
	"strings"
	"testing"
)

// TestSpecializeCoversEveryShaderFlag guards against drift: a FEAT_* constant
// added to the WGSL but not to Features would silently stay enabled forever, and
// one removed from the WGSL would leave a dead entry in the Go map.
func TestSpecializeCoversEveryShaderFlag(t *testing.T) {
	inShader := FlagNames(Source())
	if len(inShader) == 0 {
		t.Fatal("no FEAT_* constants found in the linked shader")
	}

	known := map[string]bool{"FEAT_TERRAIN": true, "FEAT_WATER": true, "FEAT_CAMPFIRE": true}
	for _, n := range primFlagNames {
		known[n] = true
	}

	var missing []string
	seen := map[string]bool{}
	for _, n := range inShader {
		seen[n] = true
		if !known[n] {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("shader declares %v with no entry in Features; they will never be stripped", missing)
	}
	for n := range known {
		if !seen[n] {
			t.Errorf("Features maps %s but the shader no longer declares it", n)
		}
	}
}

// TestSpecializeRewritesOnlyRequestedFlags checks the rewrite is exact: the
// selected constants flip and nothing else in the shader source moves.
func TestSpecializeRewritesOnlyRequestedFlags(t *testing.T) {
	src := Source()
	full := Specialize(src, AllFeatures())

	f := AllFeatures()
	f.Terrain = false
	f.Prim[5] = false // torus
	got := Specialize(src, f)

	if !strings.Contains(got, "const FEAT_TERRAIN: bool = false;") {
		t.Error("FEAT_TERRAIN was not disabled")
	}
	if !strings.Contains(got, "const FEAT_PRIM_TORUS: bool = false;") {
		t.Error("FEAT_PRIM_TORUS was not disabled")
	}
	if !strings.Contains(got, "const FEAT_WATER: bool = true;") {
		t.Error("FEAT_WATER should have been left enabled")
	}

	// Only the two requested declarations may differ from the all-on source.
	if diff := countDifferingLines(full, got); diff != 2 {
		t.Errorf("specialization changed %d lines, want exactly 2", diff)
	}
}

// TestAllFeaturesMatchesCheckedInShader pins the invariant that the linked WGSL
// on disk is the full-featured build, so a run with specialization disabled and
// a run before the first frame behave identically.
func TestAllFeaturesMatchesCheckedInShader(t *testing.T) {
	src := Source()
	if got := Specialize(src, AllFeatures()); got != src {
		t.Error("checked-in shader is not the all-features build; " +
			"every FEAT_* declaration in types.wesl must default to true")
	}
}

func countDifferingLines(a, b string) int {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(la) != len(lb) {
		return -1
	}
	n := 0
	for i := range la {
		if la[i] != lb[i] {
			n++
		}
	}
	return n
}
