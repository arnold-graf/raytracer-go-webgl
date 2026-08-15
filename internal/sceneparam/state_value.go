package sceneparam

import "fmt"

// StateValue is an exported snapshot of a reactive state variable.
type StateValue struct {
	Kind    string
	Number  float64
	String  string
	Boolean bool
	Vec3    [3]float64
}

func valueToState(v value) StateValue {
	sv := StateValue{Kind: v.describe()}
	switch v.kind {
	case valNumber:
		sv.Number = v.number
	case valString:
		sv.String = v.str
	case valBool:
		sv.Boolean = v.boolean
	case valVec3:
		sv.Vec3 = v.vec3
	}
	return sv
}

func stateToValue(s StateValue) (value, error) {
	switch s.Kind {
	case "number", "":
		return value{kind: valNumber, number: s.Number}, nil
	case "string":
		return value{kind: valString, str: s.String}, nil
	case "bool":
		return value{kind: valBool, boolean: s.Boolean}, nil
	case "vec3":
		return value{kind: valVec3, vec3: s.Vec3}, nil
	default:
		return value{}, fmt.Errorf("unsupported state kind %q", s.Kind)
	}
}

// ExportState converts internal state snapshots for scene loading.
func ExportState(in map[string]value) map[string]StateValue {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]StateValue, len(in))
	for k, v := range in {
		out[k] = valueToState(v)
	}
	return out
}

// EvalExpr evaluates an expression against env.
func EvalExpr(src string, env *Env) (StateValue, error) {
	v, err := evalExpr(src, env)
	if err != nil {
		return StateValue{}, err
	}
	return valueToState(v), nil
}

// EvalNumber evaluates an expression to a number.
func EvalNumber(src string, env *Env) (float64, error) {
	v, err := evalExpr(src, env)
	if err != nil {
		return 0, err
	}
	return v.asNumber()
}

// BuildEnv creates an evaluation env from scoped state values.
func BuildEnv(scoped map[string]StateValue) (*Env, error) {
	env := NewEnv()
	for key, sv := range scoped {
		v, err := stateToValue(sv)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		env.Set(key, v)
	}
	return env, nil
}

// ImportStateValue converts an exported state value for env storage.
func ImportStateValue(s StateValue) (value, error) {
	return stateToValue(s)
}

// LookupState reads a variable as an exported value.
func (e *Env) LookupState(name string) (StateValue, bool) {
	v, ok := e.Lookup(name)
	if !ok {
		return StateValue{}, false
	}
	return valueToState(v), true
}
