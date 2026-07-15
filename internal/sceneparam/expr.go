package sceneparam

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type valueKind int

const (
	valNumber valueKind = iota
	valString
	valBool
	valVec3
	valStrings
)

type value struct {
	kind    valueKind
	number  float64
	str     string
	boolean bool
	vec3    [3]float64
	strings []string
}

func (v value) truthy() bool {
	switch v.kind {
	case valString:
		return v.str != ""
	case valBool:
		return v.boolean
	case valVec3:
		return true
	case valStrings:
		return len(v.strings) > 0
	default:
		return v.number != 0
	}
}

func (v value) asNumber() (float64, error) {
	if v.kind == valNumber {
		return v.number, nil
	}
	return 0, fmt.Errorf("expected number, got %s", v.describe())
}

func (v value) describe() string {
	switch v.kind {
	case valString:
		return "string"
	case valBool:
		return "bool"
	case valVec3:
		return "vec3"
	case valStrings:
		return "string array"
	default:
		return "number"
	}
}

func (v value) toAny() any {
	switch v.kind {
	case valString:
		return v.str
	case valBool:
		return v.boolean
	case valVec3:
		return []float64{v.vec3[0], v.vec3[1], v.vec3[2]}
	case valStrings:
		out := make([]string, len(v.strings))
		copy(out, v.strings)
		return out
	default:
		return v.number
	}
}

// Env holds props, consts, and loop-local bindings for expression evaluation.
type Env struct {
	vars map[string]value
}

func NewEnv() *Env {
	return &Env{vars: make(map[string]value)}
}

func (e *Env) Set(name string, v value) {
	e.vars[name] = v
}

func (e *Env) SetAny(name string, v any) error {
	val, err := anyToValue(v)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	e.Set(name, val)
	return nil
}

func (e *Env) Lookup(name string) (value, bool) {
	v, ok := e.vars[name]
	return v, ok
}

func (e *Env) Child() *Env {
	child := NewEnv()
	for k, v := range e.vars {
		child.vars[k] = v
	}
	return child
}

func anyToValue(v any) (value, error) {
	switch x := v.(type) {
	case float64:
		return value{kind: valNumber, number: x}, nil
	case int64:
		return value{kind: valNumber, number: float64(x)}, nil
	case int:
		return value{kind: valNumber, number: float64(x)}, nil
	case bool:
		return value{kind: valBool, boolean: x}, nil
	case string:
		if strings.HasPrefix(x, "'") && strings.HasSuffix(x, "'") && len(x) >= 2 {
			inner := strings.TrimSpace(x[1 : len(x)-1])
			ev, err := evalExpr(inner, NewEnv())
			if err != nil {
				return value{}, err
			}
			return ev, nil
		}
		return value{kind: valString, str: x}, nil
	case []any:
		if len(x) == 0 {
			return value{kind: valStrings, strings: nil}, nil
		}
		if len(x) == 3 {
			if f0, ok0 := toFloat64(x[0]); ok0 {
				if f1, ok1 := toFloat64(x[1]); ok1 {
					if f2, ok2 := toFloat64(x[2]); ok2 {
						return value{kind: valVec3, vec3: [3]float64{f0, f1, f2}}, nil
					}
				}
			}
		}
		out := make([]string, len(x))
		for i, item := range x {
			switch s := item.(type) {
			case string:
				out[i] = s
			default:
				f, ok := toFloat64(item)
				if !ok {
					return value{}, fmt.Errorf("unsupported array element %T", item)
				}
				out[i] = strconv.FormatFloat(f, 'g', -1, 64)
			}
		}
		return value{kind: valStrings, strings: out}, nil
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return value{kind: valStrings, strings: out}, nil
	case []float64:
		if len(x) == 3 {
			return value{kind: valVec3, vec3: [3]float64{x[0], x[1], x[2]}}, nil
		}
	}
	return value{}, fmt.Errorf("unsupported value type %T", v)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func evalExpr(src string, env *Env) (value, error) {
	p := &parser{src: strings.TrimSpace(src), env: env}
	v, err := p.parseExpr()
	if err != nil {
		return value{}, err
	}
	if strings.TrimSpace(p.src) != "" {
		return value{}, fmt.Errorf("unexpected trailing input in %q", src)
	}
	return v, nil
}

type parser struct {
	src string
	env *Env
}

func (p *parser) skipSpace() {
	p.src = strings.TrimLeftFunc(p.src, unicode.IsSpace)
}

func (p *parser) parseExpr() (value, error) {
	left, err := p.parseTerm()
	if err != nil {
		return value{}, err
	}
	for {
		p.skipSpace()
		if len(p.src) == 0 {
			return left, nil
		}
		switch p.src[0] {
		case '+':
			p.src = p.src[1:]
			right, err := p.parseTerm()
			if err != nil {
				return value{}, err
			}
			a, err := left.asNumber()
			if err != nil {
				return value{}, err
			}
			b, err := right.asNumber()
			if err != nil {
				return value{}, err
			}
			left = value{kind: valNumber, number: a + b}
		case '-':
			p.src = p.src[1:]
			right, err := p.parseTerm()
			if err != nil {
				return value{}, err
			}
			a, err := left.asNumber()
			if err != nil {
				return value{}, err
			}
			b, err := right.asNumber()
			if err != nil {
				return value{}, err
			}
			left = value{kind: valNumber, number: a - b}
		default:
			return left, nil
		}
	}
}

func (p *parser) parseTerm() (value, error) {
	left, err := p.parseUnary()
	if err != nil {
		return value{}, err
	}
	for {
		p.skipSpace()
		if len(p.src) == 0 {
			return left, nil
		}
		switch p.src[0] {
		case '*':
			p.src = p.src[1:]
			right, err := p.parseUnary()
			if err != nil {
				return value{}, err
			}
			a, err := left.asNumber()
			if err != nil {
				return value{}, err
			}
			b, err := right.asNumber()
			if err != nil {
				return value{}, err
			}
			left = value{kind: valNumber, number: a * b}
		case '/':
			p.src = p.src[1:]
			right, err := p.parseUnary()
			if err != nil {
				return value{}, err
			}
			a, err := left.asNumber()
			if err != nil {
				return value{}, err
			}
			b, err := right.asNumber()
			if err != nil {
				return value{}, err
			}
			left = value{kind: valNumber, number: a / b}
		default:
			return left, nil
		}
	}
}

func (p *parser) parseUnary() (value, error) {
	p.skipSpace()
	if strings.HasPrefix(p.src, "-") {
		p.src = p.src[1:]
		v, err := p.parseUnary()
		if err != nil {
			return value{}, err
		}
		n, err := v.asNumber()
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: -n}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (value, error) {
	p.skipSpace()
	if len(p.src) == 0 {
		return value{}, fmt.Errorf("unexpected end of expression")
	}
	if p.src[0] == '(' {
		p.src = p.src[1:]
		v, err := p.parseExpr()
		if err != nil {
			return value{}, err
		}
		p.skipSpace()
		if !strings.HasPrefix(p.src, ")") {
			return value{}, fmt.Errorf("expected ')'")
		}
		p.src = p.src[1:]
		return v, nil
	}
	if unicode.IsDigit(rune(p.src[0])) || p.src[0] == '.' {
		return p.parseNumber()
	}
	if unicode.IsLetter(rune(p.src[0])) || p.src[0] == '_' {
		return p.parseIdentOrCall()
	}
	return value{}, fmt.Errorf("unexpected character %q", p.src[0])
}

func (p *parser) parseNumber() (value, error) {
	i := 0
	for i < len(p.src) && (unicode.IsDigit(rune(p.src[i])) || p.src[i] == '.') {
		i++
	}
	n, err := strconv.ParseFloat(p.src[:i], 64)
	if err != nil {
		return value{}, err
	}
	p.src = p.src[i:]
	return value{kind: valNumber, number: n}, nil
}

func (p *parser) parseIdentOrCall() (value, error) {
	i := 0
	for i < len(p.src) && (unicode.IsLetter(rune(p.src[i])) || unicode.IsDigit(rune(p.src[i])) || p.src[i] == '_') {
		i++
	}
	name := p.src[:i]
	p.src = p.src[i:]
	p.skipSpace()
	if strings.HasPrefix(p.src, "(") {
		p.src = p.src[1:]
		args, err := p.parseArgs()
		if err != nil {
			return value{}, err
		}
		p.skipSpace()
		if !strings.HasPrefix(p.src, ")") {
			return value{}, fmt.Errorf("expected ')' after call to %s", name)
		}
		p.src = p.src[1:]
		return evalCall(name, args)
	}
	if v, ok := p.env.Lookup(name); ok {
		return v, nil
	}
	return value{}, fmt.Errorf("unknown name %q", name)
}

func (p *parser) parseArgs() ([]value, error) {
	p.skipSpace()
	if strings.HasPrefix(p.src, ")") {
		return nil, nil
	}
	var args []value
	for {
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		p.skipSpace()
		if strings.HasPrefix(p.src, ")") {
			return args, nil
		}
		if !strings.HasPrefix(p.src, ",") {
			return nil, fmt.Errorf("expected ',' or ')' in argument list")
		}
		p.src = p.src[1:]
	}
}

func evalCall(name string, args []value) (value, error) {
	nums := func(n int) ([]float64, error) {
		if len(args) != n {
			return nil, fmt.Errorf("%s wants %d args, got %d", name, n, len(args))
		}
		out := make([]float64, n)
		for i, a := range args {
			f, err := a.asNumber()
			if err != nil {
				return nil, fmt.Errorf("%s arg %d: %w", name, i, err)
			}
			out[i] = f
		}
		return out, nil
	}
	switch name {
	case "floor":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: float64(int(ns[0]))}, nil
	case "leg_x":
		ns, err := nums(3)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: legX(int(ns[0]), ns[1], ns[2])}, nil
	case "leg_z":
		ns, err := nums(3)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: legZ(int(ns[0]), ns[1], ns[2])}, nil
	case "ring_lerp":
		ns, err := nums(4)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: ringLerp(int(ns[0]), int(ns[1]), ns[2], ns[3])}, nil
	default:
		return value{}, fmt.Errorf("unknown function %q", name)
	}
}

func formatTOML(v value) (string, error) {
	switch v.kind {
	case valNumber:
		return strconv.FormatFloat(v.number, 'g', -1, 64), nil
	case valString:
		return strconv.Quote(v.str), nil
	case valBool:
		if v.boolean {
			return "true", nil
		}
		return "false", nil
	case valVec3:
		return fmt.Sprintf("[%g, %g, %g]", v.vec3[0], v.vec3[1], v.vec3[2]), nil
	case valStrings:
		if len(v.strings) == 0 {
			return "[]", nil
		}
		parts := make([]string, len(v.strings))
		for i, s := range v.strings {
			parts[i] = strconv.Quote(s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		return "", fmt.Errorf("cannot format value")
	}
}
