package sceneio

import (
	"path/filepath"

	"raytracer/internal/scene"
)

// LoadIncludeSubScene loads an included file the same way mergeInclude does
// (merging nested includes) for bounds and migration math.
func LoadIncludeSubScene(absPath string, params map[string]any) (*scene.Scene, error) {
	return load(absPath, params, map[string]bool{}, nil, nil)
}

// DecodeSceneIncludes reads [[include]] tables from a TOML file.
func DecodeSceneIncludes(path string) ([]includeDTO, error) {
	dto, _, err := decodeSceneFile(path, nil)
	if err != nil {
		return nil, err
	}
	return dto.Include, nil
}

// ResolveIncludePath resolves an include file path relative to parentDir.
func ResolveIncludePath(parentDir, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(parentDir, file)
}
