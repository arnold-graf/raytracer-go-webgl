package sceneparam

import (
	"os"
	"regexp"
	"strings"
)

// IncludeStatePropDep records an [[include]] prop wired to a parent [state] key.
type IncludeStatePropDep struct {
	Prop     string
	StateKey string
}

var reIncludePropPair = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*'([^']*)'`)

// IncludeStatePropDeps returns prop→state bindings for the includeIndex-th [[include]] in path.
func IncludeStatePropDeps(path string, includeIndex int) ([]IncludeStatePropDep, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_, _, state, err := parseMetaTables(string(raw))
	if err != nil {
		return nil, err
	}
	if len(state) == 0 {
		return nil, nil
	}
	stateNames := map[string]struct{}{}
	for name := range state {
		stateNames[name] = struct{}{}
	}
	block, ok := includeBlock(string(raw), includeIndex)
	if !ok || !strings.Contains(block, "props") {
		return nil, nil
	}
	return parseIncludePropStateDeps(block, stateNames), nil
}

func includeBlock(raw string, index int) (string, bool) {
	lines := strings.Split(raw, "\n")
	var blocks []string
	in := false
	var cur []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "[[include]]" {
			if in {
				blocks = append(blocks, strings.Join(cur, "\n"))
				cur = nil
			}
			in = true
			cur = append(cur, line)
			continue
		}
		if in {
			if strings.HasPrefix(trim, "[[") && trim != "[[include]]" {
				blocks = append(blocks, strings.Join(cur, "\n"))
				cur = nil
				in = false
			} else {
				cur = append(cur, line)
			}
		}
	}
	if in && len(cur) > 0 {
		blocks = append(blocks, strings.Join(cur, "\n"))
	}
	if index < 0 || index >= len(blocks) {
		return "", false
	}
	return blocks[index], true
}

func parseIncludePropStateDeps(block string, stateNames map[string]struct{}) []IncludeStatePropDep {
	var out []IncludeStatePropDep
	for _, m := range reIncludePropPair.FindAllStringSubmatch(block, -1) {
		prop := m[1]
		expr := m[2]
		if strings.Contains(expr, "(") || strings.Contains(expr, "=") {
			continue
		}
		if !isSimpleIdent(expr) {
			continue
		}
		if _, ok := stateNames[expr]; !ok {
			continue
		}
		out = append(out, IncludeStatePropDep{Prop: prop, StateKey: expr})
	}
	return out
}
