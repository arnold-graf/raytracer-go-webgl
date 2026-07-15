package sceneparam

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// NeedsExpand reports whether raw uses the v2 parameter syntax.
func NeedsExpand(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "[props]") ||
		strings.Contains(s, "[const]") ||
		strings.Contains(s, "# for ")
}

// Expand renders a parameterized object file to plain scene TOML.
func Expand(path string, raw []byte, params map[string]any) ([]byte, error) {
	rendered, _, err := ExpandWithResolved(path, raw, params)
	return rendered, err
}

// ExpandWithResolved is Expand and also returns resolved [props] values after
// merging include params (nil when the file has no [props] table).
func ExpandWithResolved(path string, raw []byte, params map[string]any) ([]byte, map[string]any, error) {
	if bytes.Contains(raw, []byte("{{")) {
		return nil, nil, fmt.Errorf("%s: Go template syntax is not allowed in parameterized objects", path)
	}
	if !NeedsExpand(raw) {
		return raw, nil, nil
	}

	props, consts, err := parseMetaTables(string(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	env := NewEnv()
	if err := mergeParams(env, props, params); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := evalConsts(env, consts); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	resolved := resolvedPropsFromEnv(env, props)

	body := stripMetaTables(string(raw))
	body, err = expandDirectives(body, env)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	body, err = substituteExprs(body, env)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return []byte(body), resolved, nil
}

func resolvedPropsFromEnv(env *Env, props map[string]metaEntry) map[string]any {
	if len(props) == 0 {
		return nil
	}
	out := make(map[string]any, len(props))
	for key := range props {
		if v, ok := env.Lookup(key); ok {
			out[key] = v.toAny()
		}
	}
	return out
}

type metaEntry struct {
	expr   string // non-empty => evaluate as expression
	literal string
	isExpr bool
}

func parseMetaTables(raw string) (props map[string]metaEntry, consts map[string]metaEntry, err error) {
	props = map[string]metaEntry{}
	consts = map[string]metaEntry{}
	section := ""
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "[props]" {
			section = "props"
			continue
		}
		if trim == "[const]" {
			section = "const"
			continue
		}
		if strings.HasPrefix(trim, "[") && trim != "" {
			if section != "" {
				section = ""
			}
			continue
		}
		if section == "" || trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		key, entry, err := parseMetaLine(trim)
		if err != nil {
			return nil, nil, err
		}
		switch section {
		case "props":
			props[key] = entry
		case "const":
			consts[key] = entry
		}
	}
	return props, consts, nil
}

func parseMetaLine(line string) (string, metaEntry, error) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return "", metaEntry{}, fmt.Errorf("invalid meta line %q", line)
	}
	key := strings.TrimSpace(line[:eq])
	val := strings.TrimSpace(line[eq+1:])
	if strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") && len(val) >= 2 {
		return key, metaEntry{expr: val[1 : len(val)-1], isExpr: true}, nil
	}
	return key, metaEntry{literal: val, isExpr: false}, nil
}

func mergeParams(env *Env, props map[string]metaEntry, params map[string]any) error {
	merged := map[string]metaEntry{}
	for k, v := range props {
		merged[k] = v
	}
	if params != nil {
		for k, v := range params {
			entry, err := paramToEntry(v)
			if err != nil {
				return fmt.Errorf("param %q: %w", k, err)
			}
			merged[k] = entry
		}
		if _, ok := merged["albedo"]; !ok {
			if p, ok := params["chrome_albedo"]; ok {
				entry, err := paramToEntry(p)
				if err != nil {
					return err
				}
				merged["albedo"] = entry
			}
		}
	}
	for key, entry := range merged {
		if !entry.isExpr {
			if err := applyMetaEntry(env, key, entry); err != nil {
				return err
			}
		}
	}
	pending := map[string]metaEntry{}
	for key, entry := range merged {
		if entry.isExpr {
			pending[key] = entry
		}
	}
	for len(pending) > 0 {
		progress := false
		for key, entry := range pending {
			v, err := evalExpr(entry.expr, env)
			if err != nil {
				if isUnknownName(err) {
					continue
				}
				return fmt.Errorf("prop %s: %w", key, err)
			}
			env.Set(key, v)
			delete(pending, key)
			progress = true
		}
		if !progress {
			names := make([]string, 0, len(pending))
			for k := range pending {
				names = append(names, k)
			}
			return fmt.Errorf("prop cycle or unresolved refs: %s", strings.Join(names, ", "))
		}
	}
	return nil
}

func paramToEntry(v any) (metaEntry, error) {
	switch x := v.(type) {
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return metaEntry{literal: strconv.FormatFloat(f, 'g', -1, 64), isExpr: false}, nil
		}
		return metaEntry{literal: strconv.Quote(x), isExpr: false}, nil
	default:
		b, err := toml.Marshal(map[string]any{"v": v})
		if err != nil {
			return metaEntry{}, err
		}
		s := strings.TrimSpace(string(b))
		val := strings.TrimPrefix(s, "v = ")
		return metaEntry{literal: val, isExpr: false}, nil
	}
}

func applyMetaEntry(env *Env, key string, entry metaEntry) error {
	if entry.isExpr {
		v, err := evalExpr(entry.expr, env)
		if err != nil {
			return fmt.Errorf("%s = %q: %w", key, entry.expr, err)
		}
		env.Set(key, v)
		return nil
	}
	var holder map[string]any
	if _, err := toml.Decode("k = "+entry.literal, &holder); err != nil {
		return fmt.Errorf("%s = %s: %w", key, entry.literal, err)
	}
	return env.SetAny(key, holder["k"])
}

func evalConsts(env *Env, consts map[string]metaEntry) error {
	pending := make(map[string]metaEntry, len(consts))
	for k, v := range consts {
		pending[k] = v
	}
	for len(pending) > 0 {
		progress := false
		for key, entry := range pending {
			if entry.isExpr {
				v, err := evalExpr(entry.expr, env)
				if err != nil {
					if isUnknownName(err) {
						continue
					}
					return fmt.Errorf("const %s: %w", key, err)
				}
				env.Set(key, v)
				delete(pending, key)
				progress = true
				continue
			}
			if err := applyMetaEntry(env, key, entry); err != nil {
				return fmt.Errorf("const %s: %w", key, err)
			}
			delete(pending, key)
			progress = true
		}
		if !progress {
			names := make([]string, 0, len(pending))
			for k := range pending {
				names = append(names, k)
			}
			return fmt.Errorf("const cycle or unresolved refs: %s", strings.Join(names, ", "))
		}
	}
	return nil
}

func isUnknownName(err error) bool {
	return strings.Contains(err.Error(), "unknown name")
}

func stripMetaTables(raw string) string {
	lines := strings.Split(raw, "\n")
	var out []string
	section := ""
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "[props]" || trim == "[const]" {
			section = trim
			continue
		}
		if section != "" {
			if strings.HasPrefix(trim, "[[") || strings.HasPrefix(trim, "# for") || strings.HasPrefix(trim, "# if") {
				section = ""
			} else if trim == "" || strings.HasPrefix(trim, "#") || strings.Contains(trim, "=") {
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func substituteExprs(text string, env *Env) (string, error) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		newLine, err := substituteLine(line, env)
		if err != nil {
			return "", fmt.Errorf("line %d: %w", i+1, err)
		}
		lines[i] = newLine
	}
	return strings.Join(lines, "\n"), nil
}

func substituteLine(line string, env *Env) (string, error) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line, nil
	}
	prefix := line[:eq+1]
	rest := strings.TrimSpace(line[eq+1:])
	if !strings.Contains(rest, "'") {
		return line, nil
	}
	replaced, err := replaceQuotedExprs(rest, env)
	if err != nil {
		return "", err
	}
	return prefix + " " + replaced, nil
}

func replaceQuotedExprs(s string, env *Env) (string, error) {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '\'' {
			out.WriteByte(s[i])
			i++
			continue
		}
		j := i + 1
		for j < len(s) && s[j] != '\'' {
			j++
		}
		if j >= len(s) {
			return "", fmt.Errorf("unterminated string in %q", s)
		}
		expr := s[i+1 : j]
		v, err := evalExpr(expr, env)
		if err != nil {
			return "", fmt.Errorf("%q: %w", expr, err)
		}
		formatted, err := formatTOML(v)
		if err != nil {
			return "", err
		}
		out.WriteString(formatted)
		i = j + 1
	}
	return out.String(), nil
}
