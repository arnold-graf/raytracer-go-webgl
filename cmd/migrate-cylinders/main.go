// migrate-cylinders rewrites [[cylinder]] tables from cx/cz/radius/ymin/ymax
// to pos_x/pos_y/pos_z/width/height (and width_top/width_bottom when tapered).
//
// Usage: go run ./cmd/migrate-cylinders [-w] [paths...]
// Paths default to scenes/. Skips blocks containing {{ template actions.
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
)

var (
	reKV     = regexp.MustCompile(`^(\s*)([A-Za-z0-9_]+)\s*=\s*(.+?)\s*$`)
	reHeader = regexp.MustCompile(`^\s*\[\[cylinder\]\]`)
)

func main() {
	write := flag.Bool("w", false, "write changes to files (default: dry run)")
	flag.Parse()
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
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out, n, err := migrate(string(data))
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if n == 0 {
				return nil
			}
			changed += n
			if *write {
				if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
					return err
				}
				fmt.Printf("updated %s (%d cylinders)\n", path, n)
			} else {
				fmt.Printf("would update %s (%d cylinders)\n", path, n)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
	if changed == 0 {
		fmt.Println("no cylinders to migrate")
	}
}

func migrate(src string) (string, int, error) {
	lines := strings.Split(src, "\n")
	var out []string
	changed := 0
	for i := 0; i < len(lines); {
		if !reHeader.MatchString(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		header := lines[i]
		i++
		var block []string
		for i < len(lines) {
			line := lines[i]
			if strings.TrimSpace(line) != "" && strings.HasPrefix(strings.TrimSpace(line), "[[") {
				break
			}
			block = append(block, line)
			i++
		}
		newBlock, ok, err := convertBlock(header, block)
		if err != nil {
			return "", 0, fmt.Errorf("block: %w", err)
		}
		if ok {
			changed++
		}
		out = append(out, newBlock...)
	}
	text := strings.Join(out, "\n")
	if strings.HasSuffix(src, "\n") && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, changed, nil
}

func convertBlock(header string, block []string) ([]string, bool, error) {
	fields := map[string]string{}
	var leading, trailing, other []string
	seenKV := false
	skip := map[string]bool{
		"cx": true, "cz": true, "radius": true, "radius_top": true,
		"ymin": true, "ymax": true,
	}
	for _, line := range block {
		trim := strings.TrimSpace(line)
		if trim == "" {
			if !seenKV {
				leading = append(leading, line)
			} else {
				trailing = append(trailing, line)
			}
			continue
		}
		if strings.HasPrefix(trim, "#") {
			if !seenKV {
				leading = append(leading, line)
			} else {
				trailing = append(trailing, line)
			}
			continue
		}
		m := reKV.FindStringSubmatch(line)
		if m == nil {
			if !seenKV {
				leading = append(leading, line)
			} else {
				trailing = append(trailing, line)
			}
			continue
		}
		seenKV = true
		fields[m[2]] = m[3]
		if !skip[m[2]] {
			other = append(other, line)
		}
	}
	if _, ok := fields["cx"]; !ok {
		return append([]string{header}, block...), false, nil
	}
	for _, v := range fields {
		if strings.Contains(v, "{{") {
			return append([]string{header}, block...), false, nil
		}
	}
	cx, err := parseNum(fields["cx"])
	if err != nil {
		return nil, false, err
	}
	cz, err := parseNum(fields["cz"])
	if err != nil {
		return nil, false, err
	}
	r, err := parseNum(fields["radius"])
	if err != nil {
		return nil, false, err
	}
	ymin, err := parseNum(fields["ymin"])
	if err != nil {
		return nil, false, err
	}
	ymax, err := parseNum(fields["ymax"])
	if err != nil {
		return nil, false, err
	}
	rt := r
	if v, ok := fields["radius_top"]; ok {
		rt, err = parseNum(v)
		if err != nil {
			return nil, false, err
		}
		if rt == 0 {
			rt = r
		}
	}
	wBot := 2 * r
	wTop := 2 * rt
	foot := wBot
	if wTop > foot {
		foot = wTop
	}
	posX := cx - foot/2
	posZ := cz - foot/2
	height := ymax - ymin

	var out []string
	out = append(out, header)
	out = append(out, leading...)
	out = append(out, fmt.Sprintf("pos_x = %s", fmtNum(posX)))
	out = append(out, fmt.Sprintf("pos_y = %s", fmtNum(ymin)))
	out = append(out, fmt.Sprintf("pos_z = %s", fmtNum(posZ)))
	if math.Abs(wBot-wTop) < 1e-12 {
		out = append(out, fmt.Sprintf("width = %s", fmtNum(wBot)))
	} else {
		out = append(out, fmt.Sprintf("width_bottom = %s", fmtNum(wBot)))
		out = append(out, fmt.Sprintf("width_top = %s", fmtNum(wTop)))
	}
	out = append(out, fmt.Sprintf("height = %s", fmtNum(height)))
	out = append(out, other...)
	out = append(out, trailing...)
	return out, true, nil
}

func parseNum(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

func fmtNum(x float64) string {
	if math.Abs(x-math.Round(x)) < 1e-9 {
		return strconv.FormatInt(int64(math.Round(x)), 10)
	}
	return strconv.FormatFloat(x, 'g', -1, 64)
}
