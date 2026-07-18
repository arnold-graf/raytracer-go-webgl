package shaders

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var modulesSHA256Re = regexp.MustCompile(`(?m)^// modules-sha256: ([0-9a-f]{64})$`)

// ModulesSHA256 returns the digest link.sh stamps into trace_linked.wgsl:
// sorted modules/*.wesl paths, each as "<path>\n<content>".
func ModulesSHA256(shaderDir string) (string, error) {
	files, err := filepath.Glob(filepath.Join(shaderDir, "modules", "*.wesl"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no modules/*.wesl in %s", shaderDir)
	}
	h := sha256.New()
	for _, f := range files {
		rel, err := filepath.Rel(shaderDir, f)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\n", filepath.ToSlash(rel))
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// StampedSHA256 parses the modules-sha256 stamp from linked WGSL text.
func StampedSHA256(wgsl string) (string, bool) {
	m := modulesSHA256Re.FindStringSubmatch(wgsl)
	if m == nil {
		return "", false
	}
	return m[1], true
}
