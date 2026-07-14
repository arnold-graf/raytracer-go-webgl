package sceneio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultFontFile = "PixelOperator.ttf"

// resolveFontPath maps a TOML font name to an absolute path under the repo's
// assets/ directory. Accepts "M42.ttf", "assets/M42.ttf", or legacy relative
// paths that end in assets/<file>. Absolute paths are returned unchanged.
func resolveFontPath(name string) (string, error) {
	if name == "" {
		name = defaultFontFile
	}
	if filepath.IsAbs(name) {
		return name, nil
	}
	slash := filepath.ToSlash(name)
	if i := strings.LastIndex(slash, "assets/"); i >= 0 {
		name = slash[i+len("assets/"):]
	} else {
		name = filepath.Base(name)
	}
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "assets", name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("font %q not found in assets/ (%s)", name, path)
	}
	return path, nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}
