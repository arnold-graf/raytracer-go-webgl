package shaders

import (
	"os"
	"path/filepath"
	"testing"
)

const regenerateHint = "regenerate with: go generate ./internal/webgpu/shaders (or run the app — it auto-links stale modules)"

// TestLinkedWGSLMatchesModules guards against editing modules/*.wesl without
// re-linking: link.sh stamps trace_linked.wgsl with a digest of the module
// sources, and this test recomputes it. It needs no linker toolchain, so it
// runs everywhere go test does.
func TestLinkedWGSLMatchesModules(t *testing.T) {
	stamped, ok := StampedSHA256(linkedWGSL)
	if !ok {
		t.Fatalf("trace_linked.wgsl has no modules-sha256 stamp; %s", regenerateHint)
	}

	got, err := ModulesSHA256(".")
	if err != nil {
		t.Fatal(err)
	}
	if got != stamped {
		t.Fatalf("trace_linked.wgsl is stale: modules digest %s does not match stamped %s; %s",
			got, stamped, regenerateHint)
	}

	// Sanity: on-disk copy matches embed when tests run from the shaders package.
	path := filepath.Join("trace_linked.wgsl")
	if b, err := os.ReadFile(path); err == nil {
		if disk, ok := StampedSHA256(string(b)); ok && disk != stamped {
			t.Fatalf("trace_linked.wgsl on disk (%s) differs from embedded (%s); %s",
				disk, stamped, regenerateHint)
		}
	}
}
