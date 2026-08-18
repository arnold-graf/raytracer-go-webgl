package sceneparam

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reIfLine = regexp.MustCompile(`^#\s*if(?:\s+(not))?\s+(.+)$`)
)

func isIfLine(trim string) bool {
	return reIfLine.MatchString(trim)
}

func parseIfLine(trim string) (expr string, neg bool, err error) {
	m := reIfLine.FindStringSubmatch(trim)
	if m == nil {
		return "", false, fmt.Errorf("invalid # if %q", strings.TrimPrefix(trim, "# if "))
	}
	return strings.TrimSpace(m[2]), m[1] == "not", nil
}
