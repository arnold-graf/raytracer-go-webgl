package sceneio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// audioFileExts are the file formats a [[sound]] may name instead of a
// synthesized sound id.
var audioFileExts = map[string]bool{".mp3": true, ".ogg": true, ".wav": true}

// isAudioFile reports whether a [[sound]] id names an audio file rather than a
// synthesized sound.
func isAudioFile(name string) bool {
	return audioFileExts[strings.ToLower(filepath.Ext(name))]
}

// resolveAudioPath maps an audio file name to an absolute path under the repo's
// assets/audio/ directory. Accepts "fire.mp3", "audio/fire.mp3" or
// "assets/audio/fire.mp3". Absolute paths are returned unchanged.
func resolveAudioPath(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	slash := filepath.ToSlash(name)
	if i := strings.LastIndex(slash, "audio/"); i >= 0 {
		name = slash[i+len("audio/"):]
	} else {
		name = filepath.Base(name)
	}
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "assets", "audio", name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("audio file %q not found in assets/audio/ (%s)", name, path)
	}
	return path, nil
}
