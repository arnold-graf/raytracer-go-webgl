package webgpu

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

// TestPTFlagsMatchShader pins every Go PT flag to the literal in pt.wesl.
//
// These two lists diverged once, silently: the Go side was iota-derived, a new
// flag was inserted in the middle, and four flags swapped meanings without any
// error. Renders still looked plausible and several measurements quietly
// measured the wrong feature.
func TestPTFlagsMatchShader(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(file), "shaders", "pt", "pt.wesl"))
	if err != nil {
		t.Fatalf("read pt.wesl: %v", err)
	}
	shader := map[string]uint32{}
	re := regexp.MustCompile(`(?m)^const (PT_FLAG_[A-Z_]+): u32 = (\d+)u;`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		v, err := strconv.ParseUint(m[2], 10, 32)
		if err != nil {
			t.Fatalf("parse %s: %v", m[1], err)
		}
		shader[m[1]] = uint32(v)
	}
	if len(shader) == 0 {
		t.Fatal("no PT_FLAG_* constants found in pt.wesl")
	}

	want := map[string]uint32{
		"PT_FLAG_PHYSICAL":        ptFlagPhysical,
		"PT_FLAG_AMBIENT":         ptFlagAmbient,
		"PT_FLAG_NO_NEE":          ptFlagNoNEE,
		"PT_FLAG_NO_SKY":          ptFlagNoSky,
		"PT_FLAG_DBG_ALBEDO":      ptFlagDbgAlbedo,
		"PT_FLAG_DBG_NORMAL":      ptFlagDbgNormal,
		"PT_FLAG_DBG_DIRECT":      ptFlagDbgDirect,
		"PT_FLAG_NO_SHADOW":       ptFlagNoShadow,
		"PT_FLAG_ALL_LIGHTS":      ptFlagAllLights,
		"PT_FLAG_WHITE_NOISE":     ptFlagWhiteNoise,
		"PT_FLAG_NO_EMITTER_NEE":  ptFlagNoEmitterNEE,
		"PT_FLAG_RESTIR_TEMPORAL": ptFlagRestirTemporal,
		"PT_FLAG_DBG_INDIRECT":    ptFlagDbgIndirect,
	}
	for name, got := range shader {
		w, known := want[name]
		if !known {
			t.Errorf("pt.wesl declares %s = %d with no Go counterpart", name, got)
			continue
		}
		if w != got {
			t.Errorf("%s: pt.wesl has %d, Go has %d", name, got, w)
		}
	}
	for name := range want {
		if _, found := shader[name]; !found {
			t.Errorf("Go declares %s but pt.wesl does not", name)
		}
	}
}
