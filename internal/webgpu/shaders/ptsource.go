package shaders

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

//go:embed pt/pt_linked.wgsl
var ptLinkedWGSL string

var (
	ptSourceOnce sync.Once
	ptSourceWGSL string
	ptSHA256Re   = regexp.MustCompile(`(?m)^// pt-modules-sha256: ([0-9a-f]{64})$`)
)

// PathTraceSource returns the linked WGSL for the experimental path tracer
// (cmd/pathtrace). It is a second, independent kernel built from pt/pt.wesl
// plus the same modules/*.wesl the megakernel uses; nothing in modules/ is
// modified, and Source() is unaffected by anything here.
//
// As with Source(), a stale pt_linked.wgsl is relinked from disk rather than
// silently used: an A/B against a shader that did not actually change is the
// most common way to measure nothing at all (see docs/bounce-kernel.md).
func PathTraceSource() string {
	ptSourceOnce.Do(func() {
		ptSourceWGSL = resolvePTSource()
	})
	return ptSourceWGSL
}

func resolvePTSource() string {
	dir, err := shaderDir()
	if err != nil {
		return ptLinkedWGSL
	}
	if src, ok := readPTLinkedIfCurrent(dir); ok {
		return src
	}
	if err := runPTLink(dir); err != nil {
		log.Printf("shaders: pt/link.sh failed (%v); using embedded pt_linked.wgsl", err)
		return ptLinkedWGSL
	}
	if src, ok := readPTLinkedIfCurrent(dir); ok {
		log.Printf("shaders: regenerated pt/pt_linked.wgsl")
		return src
	}
	log.Printf("shaders: pt/pt_linked.wgsl still stale after link; using embedded shader")
	return ptLinkedWGSL
}

func readPTLinkedIfCurrent(shaderDir string) (string, bool) {
	want, err := PTSourcesSHA256(shaderDir)
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(shaderDir, "pt", "pt_linked.wgsl"))
	if err != nil {
		return "", false
	}
	src := string(b)
	m := ptSHA256Re.FindStringSubmatch(src)
	if m == nil || m[1] != want {
		return "", false
	}
	return src, true
}

// PTSourcesSHA256 mirrors the digest pt/link.sh stamps: every modules/*.wesl in
// sorted order followed by pt/pt.wesl, each as "<basename>\n<content>".
func PTSourcesSHA256(shaderDir string) (string, error) {
	files, err := filepath.Glob(filepath.Join(shaderDir, "modules", "*.wesl"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no modules/*.wesl in %s", shaderDir)
	}
	sort.Strings(files)
	files = append(files, filepath.Join(shaderDir, "pt", "pt.wesl"))
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\n", filepath.Base(f))
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func runPTLink(shaderDir string) error {
	cmd := exec.Command("sh", "link.sh")
	cmd.Dir = filepath.Join(shaderDir, "pt")
	// Both streams go to stderr. The linker's progress line is informational,
	// and a tool whose stdout is data — cmd/ambientbake emits TOML there —
	// otherwise gets "wrote pt_linked.wgsl" spliced into its output, which
	// then fails to parse somewhere far away from the cause.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
