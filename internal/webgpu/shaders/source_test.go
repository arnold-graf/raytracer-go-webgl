package shaders

import (
	"strings"
	"testing"
)

func TestSourceIsLinkedWGSL(t *testing.T) {
	src := Source()
	if !strings.Contains(src, "@compute @workgroup_size(8, 8, 1)") {
		t.Fatal("missing compute entry point")
	}
	if !strings.Contains(src, "fn main(") {
		t.Fatal("missing main")
	}
	if !strings.Contains(src, "fn ray_color(") {
		t.Fatal("missing ray_color")
	}
	if strings.Contains(src, "import package::") {
		t.Fatal("linked WGSL should not contain WESL import statements")
	}
}
