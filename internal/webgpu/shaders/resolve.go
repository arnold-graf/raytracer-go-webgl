package shaders

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	sourceOnce sync.Once
	sourceWGSL string
)

// Source returns the linked trace compute shader WGSL passed to createShaderModule.
// When modules/*.wesl are newer than trace_linked.wgsl, link.sh is run automatically
// so `go run .` picks up shader edits without a manual go generate.
func Source() string {
	sourceOnce.Do(func() {
		sourceWGSL = resolveSource()
	})
	return sourceWGSL
}

func resolveSource() string {
	dir, err := shaderDir()
	if err != nil {
		return linkedWGSL
	}
	if src, ok := readLinkedIfCurrent(dir); ok {
		return src
	}
	if err := runLink(dir); err != nil {
		log.Printf("shaders: link.sh failed (%v); using embedded trace_linked.wgsl", err)
		return linkedWGSL
	}
	if src, ok := readLinkedIfCurrent(dir); ok {
		log.Printf("shaders: regenerated trace_linked.wgsl from modules/*.wesl")
		return src
	}
	log.Printf("shaders: trace_linked.wgsl still stale after link.sh; using embedded shader")
	return linkedWGSL
}

func shaderDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", os.ErrInvalid
	}
	return filepath.Dir(file), nil
}

func readLinkedIfCurrent(shaderDir string) (string, bool) {
	want, err := ModulesSHA256(shaderDir)
	if err != nil {
		return "", false
	}
	path := filepath.Join(shaderDir, "trace_linked.wgsl")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	src := string(b)
	got, ok := StampedSHA256(src)
	if !ok || got != want {
		return "", false
	}
	return src, true
}

func runLink(shaderDir string) error {
	cmd := exec.Command("sh", "link.sh")
	cmd.Dir = shaderDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
