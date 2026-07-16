package shaders

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

const regenerateHint = "regenerate it with: go generate ./internal/webgpu/shaders"

// TestLinkedWGSLMatchesModules guards against editing modules/*.wesl without
// re-linking: link.sh stamps trace_linked.wgsl with a digest of the module
// sources, and this test recomputes it. It needs no linker toolchain, so it
// runs everywhere go test does.
func TestLinkedWGSLMatchesModules(t *testing.T) {
	m := regexp.MustCompile(`(?m)^// modules-sha256: ([0-9a-f]{64})$`).FindStringSubmatch(linkedWGSL)
	if m == nil {
		t.Fatalf("trace_linked.wgsl has no modules-sha256 stamp; %s", regenerateHint)
	}
	stamped := m[1]

	files, err := filepath.Glob("modules/*.wesl")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no modules/*.wesl files found")
	}
	// Same digest link.sh computes: sorted paths, each as "<path>\n<content>".
	// filepath.Glob returns sorted paths, matching the shell glob order.
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\n", f)
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(b)
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != stamped {
		t.Fatalf("trace_linked.wgsl is stale: modules digest %s does not match stamped %s; %s",
			got, stamped, regenerateHint)
	}
}
