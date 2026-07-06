package shaders

import (
	"strings"
	"testing"
)

func TestSourceConcatenatesModulesInOrder(t *testing.T) {
	src := Source()
	markers := []string{
		"// types.wgsl",
		"// profile.wgsl",
		"// math.wgsl",
		"// texture.wgsl",
		"// sky.wgsl",
		"// intersect.wgsl",
		"// terrain.wgsl",
		"// instance.wgsl",
		"// bvh.wgsl",
		"// shade.wgsl",
		"// trace.wgsl",
		"@compute @workgroup_size(8, 8, 1)",
		"fn main(",
	}
	for _, m := range markers {
		if !strings.Contains(src, m) {
			t.Fatalf("assembled shader missing %q", m)
		}
	}
	typesIdx := strings.Index(src, "// types.wgsl")
	traceIdx := strings.Index(src, "// trace.wgsl")
	if typesIdx >= traceIdx {
		t.Fatalf("types module should precede trace module")
	}
}
