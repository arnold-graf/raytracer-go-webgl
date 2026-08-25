package texture

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse resolves a texture name, including parameterized forms such as
// tiles(0.3, 0.1). param0 and param1 are texture-specific (tile width/height for
// tiles); both default to 1 when omitted.
func Parse(s string) (id int, param0, param1 float64, err error) {
	s = strings.TrimSpace(s)
	param0, param1 = 1, 1
	if s == "" {
		return None, 1, 1, nil
	}
	if i := strings.Index(s, "("); i >= 0 {
		if !strings.HasSuffix(s, ")") {
			return 0, 0, 0, fmt.Errorf("texture %q: missing closing parenthesis", s)
		}
		name := strings.TrimSpace(s[:i])
		args := strings.TrimSpace(s[i+1 : len(s)-1])
		switch name {
		case "tiles":
			w, h, err := parseTileSizeArgs(args)
			if err != nil {
				return 0, 0, 0, err
			}
			return Tiles, w, h, nil
		default:
			return 0, 0, 0, fmt.Errorf("unknown parameterized texture %q", name)
		}
	}
	id, ok := ID(s)
	if !ok {
		return 0, 0, 0, fmt.Errorf("unknown texture %q", s)
	}
	return id, 1, 1, nil
}

func parseTileSizeArgs(args string) (w, h float64, err error) {
	if args == "" {
		return 1, 1, nil
	}
	parts := strings.Split(args, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("tiles texture wants tiles(width, height), got %q", args)
	}
	w, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || w <= 0 {
		return 0, 0, fmt.Errorf("tiles texture width must be a positive number")
	}
	h, err = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || h <= 0 {
		return 0, 0, fmt.Errorf("tiles texture height must be a positive number")
	}
	return w, h, nil
}
