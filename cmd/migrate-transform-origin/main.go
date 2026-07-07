package main

import (
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"raytracer/internal/sceneio"
	"raytracer/internal/vec"
)

var (
	dryRun    = flag.Bool("dry-run", false, "print changes without writing files")
	roundDec  = flag.Int("round", 3, "decimal places for at values")
	originAdd = flag.String("origin-list", "", "comma-separated extra include file basenames forced to transform_origin=[0,0,0]")
)

func main() {
	flag.Parse()
	extraOrigin := parseOriginList(*originAdd)
	paths := flag.Args()
	if len(paths) == 0 {
		paths = []string{"scenes"}
	}

	var changed int
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".toml") {
				return nil
			}
			n, err := migrateFile(path, extraOrigin)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			changed += n
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
	if *dryRun {
		fmt.Printf("dry-run: %d include block(s) would change\n", changed)
	} else {
		fmt.Printf("migrated %d include block(s)\n", changed)
	}
}

func parseOriginList(s string) map[string]bool {
	out := make(map[string]bool)
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = true
		}
	}
	return out
}

type includePatch struct {
	block     includeBlock
	newAt     *vec.V
	tagOrigin bool
	file      string
	oldAt     vec.V
}

func migrateFile(path string, extraOrigin map[string]bool) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	includes, err := sceneio.DecodeSceneIncludes(path)
	if err != nil || len(includes) == 0 {
		return 0, err
	}

	parentDir := filepath.Dir(path)
	lines := strings.Split(string(data), "\n")
	blocks := findIncludeBlocks(lines)
	if len(blocks) == 0 {
		return 0, nil
	}

	var patches []includePatch
	for i, blk := range blocks {
		if i >= len(includes) {
			break
		}
		inc := includes[i]
		if inc.HasExplicitTransformOrigin() || blockIsMigratedCenter(lines, blk) {
			continue
		}
		incPath := sceneio.ResolveIncludePath(parentDir, inc.File)
		sub, err := sceneio.LoadIncludeSubScene(incPath, inc.Params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s include %q: load: %v\n", path, inc.File, err)
			continue
		}

		forceOrigin := sceneio.OriginPivotInclude(inc.File) || extraOrigin[filepath.Base(inc.File)]
		if forceOrigin {
			if blockHasOriginPivotTag(lines, blk) {
				continue
			}
			patches = append(patches, includePatch{
				block: blk, tagOrigin: true, file: inc.File, oldAt: inc.AtVec(),
			})
			continue
		}

		newAt, _, err := sceneio.MigratedIncludeAt(inc.AtVec(), inc, sub)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s include %q: %v\n", path, inc.File, err)
			continue
		}
		newAt = roundVec(newAt, *roundDec)
		if newAt == inc.AtVec() {
			continue
		}
		at := newAt
		patches = append(patches, includePatch{
			block: blk, newAt: &at, file: inc.File, oldAt: inc.AtVec(),
		})
	}

	if len(patches) == 0 {
		return 0, nil
	}

	for _, p := range patches {
		fmt.Printf("%s:%d  include → %s\n", path, p.block.start+1, p.file)
		if p.newAt != nil {
			fmt.Printf("  at %v → %v\n", p.oldAt, *p.newAt)
		}
		if p.tagOrigin {
			fmt.Println("  + transform_origin = [0, 0, 0]")
		}
	}

	if *dryRun {
		return len(patches), nil
	}

	for i := len(patches) - 1; i >= 0; i-- {
		p := patches[i]
		oldBlock := strings.Join(lines[p.block.start:p.block.end], "\n")
		newBlock := patchIncludeBlock(oldBlock, p.newAt, p.tagOrigin)
		newLines := append([]string{}, lines[:p.block.start]...)
		newLines = append(newLines, strings.Split(newBlock, "\n")...)
		newLines = append(newLines, lines[p.block.end:]...)
		lines = newLines
	}

	out := strings.Join(lines, "\n")
	if strings.HasSuffix(string(data), "\n") && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return len(patches), err
	}
	return len(patches), nil
}

type includeBlock struct {
	start, end int
}

func findIncludeBlocks(lines []string) []includeBlock {
	var blocks []includeBlock
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "[[include]]" {
			continue
		}
		start := i
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			trim := strings.TrimSpace(lines[j])
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			if strings.HasPrefix(trim, "[[") || (strings.HasPrefix(trim, "[") && !strings.HasPrefix(trim, "[[")) {
				end = j
				break
			}
		}
		blocks = append(blocks, includeBlock{start: start, end: end})
		i = end - 1
	}
	return blocks
}

func blockHasOriginPivotTag(lines []string, blk includeBlock) bool {
	for i := blk.start; i < blk.end; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "transform_origin") {
			return true
		}
	}
	return false
}

func blockIsMigratedCenter(lines []string, blk includeBlock) bool {
	for i := blk.start; i < blk.end; i++ {
		if strings.Contains(strings.TrimSpace(lines[i]), "migrated-center-at") {
			return true
		}
	}
	return false
}

var atLineRe = regexp.MustCompile(`^(\s*at\s*=\s*)\[[^\]]+\]\s*$`)

func patchIncludeBlock(block string, newAt *vec.V, addOrigin bool) string {
	lines := strings.Split(block, "\n")
	out := make([]string, 0, len(lines)+1)
	hasOrigin := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "transform_origin") {
			hasOrigin = true
		}
		if newAt != nil && atLineRe.MatchString(line) {
			indent := atLineRe.ReplaceAllString(line, "$1")
			line = fmt.Sprintf("%s%s", indent, formatVec3(*newAt))
		}
		out = append(out, line)
	}
	if addOrigin && !hasOrigin {
		insertAt := 1
		for i, line := range out {
			if strings.HasPrefix(strings.TrimSpace(line), "file") {
				insertAt = i + 1
				break
			}
		}
		newOut := append([]string{}, out[:insertAt]...)
		newOut = append(newOut, "transform_origin = [0, 0, 0]")
		newOut = append(newOut, out[insertAt:]...)
		out = newOut
	}
	if newAt != nil && !hasOrigin {
		// Mark center-default at as migrated (idempotent re-runs).
		insertAt := len(out)
		for i, line := range out {
			if strings.HasPrefix(strings.TrimSpace(line), "file") {
				insertAt = i + 1
				break
			}
		}
		newOut := append([]string{}, out[:insertAt]...)
		newOut = append(newOut, "# migrated-center-at")
		newOut = append(newOut, out[insertAt:]...)
		out = newOut
	}
	return strings.Join(out, "\n")
}

func formatVec3(v vec.V) string {
	return fmt.Sprintf("[%s, %s, %s]", fmtFloat(v.X), fmtFloat(v.Y), fmtFloat(v.Z))
}

func fmtFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		return s + ".0"
	}
	s = strings.TrimRight(s, "0")
	if strings.HasSuffix(s, ".") {
		return s + "0"
	}
	return s
}

func roundVec(v vec.V, decimals int) vec.V {
	scale := math.Pow(10, float64(decimals))
	return vec.V{
		X: math.Round(v.X*scale) / scale,
		Y: math.Round(v.Y*scale) / scale,
		Z: math.Round(v.Z*scale) / scale,
	}
}
