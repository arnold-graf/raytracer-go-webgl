package sceneparam

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	reFor   = regexp.MustCompile(`^#\s*for\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+in\s+range\((.+)\)\s*$`)
	reLet   = regexp.MustCompile(`^#\s*let\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*(.+)\s*$`)
	reIf    = regexp.MustCompile(`^#\s*if\s+not\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*$`)
	reIfPos = regexp.MustCompile(`^#\s*if\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*$`)
)

func expandDirectives(text string, env *Env) (string, error) {
	lines := strings.Split(text, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if m := reFor.FindStringSubmatch(trim); m != nil {
			end, expanded, err := expandFor(lines, i+1, m[1], m[2], env)
			if err != nil {
				return "", err
			}
			out = append(out, expanded...)
			i = end + 1
			continue
		}
		if trim == "# endif" {
			return "", fmt.Errorf("unexpected # endif at line %d", i+1)
		}
		if reIfPos.MatchString(trim) || reIf.MatchString(trim) {
			end, expanded, err := expandIf(lines, i, env)
			if err != nil {
				return "", err
			}
			out = append(out, expanded...)
			i = end + 1
			continue
		}
		out = append(out, line)
		i++
	}
	return strings.Join(out, "\n"), nil
}

func expandFor(lines []string, start int, varName, boundExpr string, env *Env) (end int, out []string, err error) {
	bound, err := evalExpr(boundExpr, env)
	if err != nil {
		return 0, nil, fmt.Errorf("for range(%s): %w", boundExpr, err)
	}
	nFloat, err := bound.asNumber()
	if err != nil {
		return 0, nil, fmt.Errorf("for range(%s): %w", boundExpr, err)
	}
	n := int(nFloat)
	if n < 0 {
		return 0, nil, fmt.Errorf("for range(%s): negative count", boundExpr)
	}
	depth := 1
	bodyStart := start
	for i := start; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if reFor.MatchString(trim) {
			depth++
		}
		if trim == "# endfor" {
			depth--
			if depth == 0 {
				body := lines[bodyStart:i]
				for k := 0; k < n; k++ {
					iterEnv := env.Child()
					iterEnv.Set(varName, value{kind: valNumber, number: float64(k)})
					iterBody, err := applyLetLines(body, iterEnv)
					if err != nil {
						return 0, nil, err
					}
					expanded, err := expandDirectives(strings.Join(iterBody, "\n"), iterEnv)
					if err != nil {
						return 0, nil, err
					}
					substituted, err := substituteExprs(expanded, iterEnv)
					if err != nil {
						return 0, nil, err
					}
					if substituted != "" {
						out = append(out, strings.Split(substituted, "\n")...)
					}
				}
				return i, out, nil
			}
		}
	}
	return 0, nil, fmt.Errorf("missing # endfor for # for %s", varName)
}

func applyLetLines(body []string, env *Env) ([]string, error) {
	var out []string
	for _, line := range body {
		trim := strings.TrimSpace(line)
		if m := reLet.FindStringSubmatch(trim); m != nil {
			v, err := evalExpr(m[2], env)
			if err != nil {
				return nil, fmt.Errorf("let %s: %w", m[1], err)
			}
			env.Set(m[1], v)
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

func expandIf(lines []string, start int, env *Env) (end int, out []string, err error) {
	trim := strings.TrimSpace(lines[start])
	neg := false
	var name string
	if m := reIf.FindStringSubmatch(trim); m != nil {
		neg = true
		name = m[1]
	} else if m := reIfPos.FindStringSubmatch(trim); m != nil {
		name = m[1]
	} else {
		return 0, nil, fmt.Errorf("invalid # if at line %d", start+1)
	}
	v, ok := env.Lookup(name)
	truth := ok && v.truthy()
	if neg {
		truth = !truth
	}
	depth := 1
	bodyStart := start + 1
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if reIfPos.MatchString(t) || reIf.MatchString(t) {
			depth++
		}
		if t == "# endif" {
			depth--
			if depth == 0 {
				if truth {
					body := lines[bodyStart:i]
					expanded, err := expandDirectives(strings.Join(body, "\n"), env)
					if err != nil {
						return 0, nil, err
					}
					substituted, err := substituteExprs(expanded, env)
					if err != nil {
						return 0, nil, err
					}
					if substituted != "" {
						out = append(out, strings.Split(substituted, "\n")...)
					}
				}
				return i, out, nil
			}
		}
	}
	return 0, nil, fmt.Errorf("missing # endif for # if %s", name)
}
