package sceneparam

import (
	"fmt"
	"strconv"
	"strings"
)

// ReactiveMeta records runtime-mutable state and bindings discovered while
// expanding a parameterized object file.
type ReactiveMeta struct {
	ScopeID        string
	SourcePath     string
	State          map[string]value
	Props          map[string]value
	StructuralDeps []string
	Bindings       []LightBrightnessBinding
	Actions        []LightAction
}

// LightBrightnessBinding drives a light's effective brightness from a state-dependent expression.
type LightBrightnessBinding struct {
	LightIndex int
	ColorBase  [3]float64
	Expr       string
	StateDeps  []string
}

// LightAction is an on_use expression bound to an interactive light in this scope.
type LightAction struct {
	LightIndex int
	OnUse      string
}

// OffsetLights shifts light indices after a scene merge.
func (m *ReactiveMeta) OffsetLights(offset int) {
	if m == nil || offset == 0 {
		return
	}
	for i := range m.Bindings {
		m.Bindings[i].LightIndex += offset
	}
	for i := range m.Actions {
		m.Actions[i].LightIndex += offset
	}
}

// ScopedKey prefixes a local state variable name with this fragment's scope id.
func (m *ReactiveMeta) ScopedKey(local string) string {
	if m == nil || m.ScopeID == "" {
		return local
	}
	return m.ScopeID + "." + local
}

type trackingEnv struct {
	*Env
	stateKeys map[string]struct{}
	reads     []string
}

func newTrackingEnv(base *Env, stateKeys map[string]struct{}) *trackingEnv {
	return &trackingEnv{Env: base, stateKeys: stateKeys}
}

func (e *trackingEnv) lookupTracked(name string) (value, bool) {
	v, ok := e.Env.Lookup(name)
	if ok {
		if _, isState := e.stateKeys[name]; isState {
			e.reads = append(e.reads, name)
		}
	}
	return v, ok
}

func (e *trackingEnv) Lookup(name string) (value, bool) {
	return e.lookupTracked(name)
}

func evalExprTracked(expr string, env *Env, stateKeys map[string]struct{}) (value, []string, error) {
	te := newTrackingEnv(env, stateKeys)
	v, err := evalExpr(expr, te)
	if err != nil {
		return value{}, nil, err
	}
	return v, uniqueStrings(te.reads), nil
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func hasStateDep(deps []string) bool {
	return len(deps) > 0
}

func substituteExprsReactive(text string, env *Env, stateKeys map[string]struct{}, meta *ReactiveMeta) (string, error) {
	lines := strings.Split(text, "\n")
	section := ""
	lightIdx := -1
	lightStart := -1

	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[[light]]") {
			section = "light"
			lightIdx++
			lightStart = i
			continue
		}
		if strings.HasPrefix(trim, "[[") {
			section = ""
			lightStart = -1
		}
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}

		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "props" {
			out, err := substitutePropsLine(line, eq, env, stateKeys)
			if err != nil {
				return "", err
			}
			lines[i] = out
			continue
		}
		if key == "on_use" {
			if out, ok, err := substituteOnUseLine(line, eq, env); ok {
				if err != nil {
					return "", err
				}
				lines[i] = out
			} else if meta != nil {
				val := strings.TrimSpace(line[eq+1:])
				val = strings.Trim(val, "'\"")
				if val != "" && section == "light" && lightIdx >= 0 {
					meta.Actions = append(meta.Actions, LightAction{
						LightIndex: lightIdx,
						OnUse:      val,
					})
				}
			}
			continue
		}

		lightColor, hasLightColor := [3]float64{}, false
		if section == "light" && lightStart >= 0 {
			lightColor, hasLightColor = lightBlockColor(lines, lightStart)
		}

		newLine, binding, err := substituteLineReactive(line, env, stateKeys, section, lightIdx, lightColor, hasLightColor)
		if err != nil {
			return "", fmt.Errorf("line %d: %w", i+1, err)
		}
		lines[i] = newLine
		if binding != nil && meta != nil {
			meta.Bindings = append(meta.Bindings, *binding)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func substituteLineReactive(line string, env *Env, stateKeys map[string]struct{}, section string, lightIdx int, lightColor [3]float64, hasLightColor bool) (string, *LightBrightnessBinding, error) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line, nil, nil
	}
	key := strings.TrimSpace(line[:eq])
	rest := strings.TrimSpace(line[eq+1:])
	if !strings.Contains(rest, "'") {
		return line, nil, nil
	}

	expr := extractSingleQuotedExpr(rest)
	if expr == "" {
		replaced, err := replaceQuotedExprs(rest, env)
		if err != nil {
			return "", nil, err
		}
		return strings.TrimSpace(line[:eq+1]) + " " + replaced, nil, nil
	}

	v, deps, err := evalExprTracked(expr, env, stateKeys)
	if err != nil {
		return "", nil, fmt.Errorf("%q: %w", expr, err)
	}
	formatted, err := formatTOML(v)
	if err != nil {
		return "", nil, err
	}
	out := strings.TrimSpace(line[:eq+1]) + " " + strings.Replace(rest, "'"+expr+"'", formatted, 1)

	var binding *LightBrightnessBinding
	if section == "light" && key == "brightness" && hasStateDep(deps) && lightIdx >= 0 && hasLightColor {
		binding = &LightBrightnessBinding{
			LightIndex: lightIdx,
			ColorBase:  lightColor,
			Expr:       expr,
			StateDeps:  deps,
		}
	}
	return out, binding, nil
}

func extractSingleQuotedExpr(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "'") {
		return ""
	}
	end := strings.Index(s[1:], "'")
	if end < 0 {
		return ""
	}
	return s[1 : end+1]
}

func lightBlockColor(lines []string, start int) ([3]float64, bool) {
	for i := start + 1; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trim, "[[") {
			break
		}
		if !strings.HasPrefix(trim, "color") {
			continue
		}
		eq := strings.Index(lines[i], "=")
		if eq < 0 {
			continue
		}
		if c, ok := parseVec3Literal(strings.TrimSpace(lines[i][eq+1:])); ok {
			return c, true
		}
	}
	return [3]float64{}, false
}

func parseVec3Literal(s string) ([3]float64, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return [3]float64{}, false
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	parts := strings.Split(inner, ",")
	if len(parts) != 3 {
		return [3]float64{}, false
	}
	var out [3]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return [3]float64{}, false
		}
		out[i] = f
	}
	return out, true
}
