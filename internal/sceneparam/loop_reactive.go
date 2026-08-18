package sceneparam

import (
	"fmt"
	"strings"
)

// expandDirectivesReactive expands control flow and records state keys that affect structure.
func expandDirectivesReactive(text string, env *Env, stateKeys map[string]struct{}, structural *map[string]struct{}) (string, error) {
	lines := strings.Split(text, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if m := reFor.FindStringSubmatch(trim); m != nil {
			trackExprDeps(m[2], env, stateKeys, structural)
			end, expanded, err := expandForReactive(lines, i+1, m[1], m[2], env, stateKeys, structural)
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
		if isIfLine(trim) {
			end, expanded, err := expandIfReactive(lines, i, env, stateKeys, structural)
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

func expandForReactive(lines []string, start int, varName, boundExpr string, env *Env, stateKeys map[string]struct{}, structural *map[string]struct{}) (end int, out []string, err error) {
	bound, err := evalExpr(boundExpr, env)
	if err != nil {
		return 0, nil, fmt.Errorf("for range(%s): %w", boundExpr, err)
	}
	trackExprDeps(boundExpr, env, stateKeys, structural)
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
					expanded, err := expandDirectivesReactive(strings.Join(iterBody, "\n"), iterEnv, stateKeys, structural)
					if err != nil {
						return 0, nil, err
					}
					substituted, err := substituteExprs(expanded, iterEnv, nil)
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

func expandIfReactive(lines []string, start int, env *Env, stateKeys map[string]struct{}, structural *map[string]struct{}) (end int, out []string, err error) {
	trim := strings.TrimSpace(lines[start])
	expr, neg, err := parseIfLine(trim)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid # if at line %d", start+1)
	}
	trackExprDeps(expr, env, stateKeys, structural)
	v, err := evalExpr(expr, env)
	if err != nil {
		return 0, nil, fmt.Errorf("# if %s: %w", expr, err)
	}
	truth := v.truthy()
	if neg {
		truth = !truth
	}
	depth := 1
	bodyStart := start + 1
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if isIfLine(t) {
			depth++
		}
		if t == "# endif" {
			depth--
			if depth == 0 {
				if truth {
					body := lines[bodyStart:i]
					expanded, err := expandDirectivesReactive(strings.Join(body, "\n"), env, stateKeys, structural)
					if err != nil {
						return 0, nil, err
					}
					substituted, err := substituteExprs(expanded, env, nil)
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
	return 0, nil, fmt.Errorf("missing # endif for # if %s", expr)
}

func trackExprDeps(expr string, env *Env, stateKeys map[string]struct{}, structural *map[string]struct{}) {
	if structural == nil {
		return
	}
	_, deps, err := evalExprTracked(expr, env, stateKeys)
	if err != nil {
		return
	}
	for _, d := range deps {
		(*structural)[d] = struct{}{}
	}
}

func structuralList(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
