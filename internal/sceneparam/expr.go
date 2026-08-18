package sceneparam

import (
	"fmt"
	"math"
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

// Vars returns a copy of all variables in the env.
func (e *Env) Vars() map[string]value {
	if e == nil {
		return nil
	}
	out := make(map[string]value, len(e.vars))
	for k, v := range e.vars {
		out[k] = v
	}
	return out
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

type envLookup interface {
	Lookup(name string) (value, bool)
}

func evalExpr(src string, env envLookup) (value, error) {
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
	env envLookup
}

func (p *parser) skipSpace() {
	p.src = strings.TrimLeftFunc(p.src, unicode.IsSpace)
}

func (p *parser) parseExpr() (value, error) {
	return p.parseTernary()
}

func (p *parser) parseTernary() (value, error) {
	left, err := p.parseCompare()
	if err != nil {
		return value{}, err
	}
	p.skipSpace()
	if len(p.src) == 0 || p.src[0] != '?' {
		return left, nil
	}
	p.src = p.src[1:]
	trueVal, err := p.parseTernary()
	if err != nil {
		return value{}, err
	}
	p.skipSpace()
	if len(p.src) == 0 || p.src[0] != ':' {
		return value{}, fmt.Errorf("expected ':' in ternary")
	}
	p.src = p.src[1:]
	falseVal, err := p.parseTernary()
	if err != nil {
		return value{}, err
	}
	if left.truthy() {
		return trueVal, nil
	}
	return falseVal, nil
}

func (p *parser) parseCompare() (value, error) {
	left, err := p.parseAdd()
	if err != nil {
		return value{}, err
	}
	for {
		p.skipSpace()
		if len(p.src) == 0 {
			return left, nil
		}
		var op string
		switch {
		case strings.HasPrefix(p.src, "=="):
			op = "=="
			p.src = p.src[2:]
		case strings.HasPrefix(p.src, "!="):
			op = "!="
			p.src = p.src[2:]
		case strings.HasPrefix(p.src, "<="):
			op = "<="
			p.src = p.src[2:]
		case strings.HasPrefix(p.src, ">="):
			op = ">="
			p.src = p.src[2:]
		case strings.HasPrefix(p.src, "<"):
			op = "<"
			p.src = p.src[1:]
		case strings.HasPrefix(p.src, ">"):
			op = ">"
			p.src = p.src[1:]
		case p.matchKeyword("is"):
			if p.matchKeyword("not") {
				op = "!="
			} else {
				op = "=="
			}
		default:
			return left, nil
		}
		right, err := p.parseAdd()
		if err != nil {
			return value{}, err
		}
		ok, err := compareValues(op, left, right)
		if err != nil {
			return value{}, err
		}
		left = value{kind: valBool, boolean: ok}
	}
}

func (p *parser) parseAdd() (value, error) {
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

func compareValues(op string, left, right value) (bool, error) {
	switch op {
	case "==":
		return valuesEqual(left, right), nil
	case "!=":
		return !valuesEqual(left, right), nil
	}
	a, err := left.asNumber()
	if err != nil {
		return false, fmt.Errorf("%s: left operand: %w", op, err)
	}
	b, err := right.asNumber()
	if err != nil {
		return false, fmt.Errorf("%s: right operand: %w", op, err)
	}
	switch op {
	case "<":
		return a < b, nil
	case ">":
		return a > b, nil
	case "<=":
		return a <= b, nil
	case ">=":
		return a >= b, nil
	default:
		return false, fmt.Errorf("unknown comparison %q", op)
	}
}

func valuesEqual(left, right value) bool {
	if left.kind != right.kind {
		if left.kind == valNumber && right.kind == valBool {
			return (left.number != 0) == right.boolean
		}
		if left.kind == valBool && right.kind == valNumber {
			return left.boolean == (right.number != 0)
		}
		return false
	}
	switch left.kind {
	case valString:
		return left.str == right.str
	case valBool:
		return left.boolean == right.boolean
	case valNumber:
		return left.number == right.number
	case valVec3:
		return left.vec3 == right.vec3
	case valStrings:
		if len(left.strings) != len(right.strings) {
			return false
		}
		for i := range left.strings {
			if left.strings[i] != right.strings[i] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (p *parser) matchKeyword(kw string) bool {
	p.skipSpace()
	if !strings.HasPrefix(p.src, kw) || !identBoundary(p.src, len(kw)) {
		return false
	}
	p.src = p.src[len(kw):]
	return true
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
	if strings.HasPrefix(p.src, "!") {
		p.src = p.src[1:]
		v, err := p.parseUnary()
		if err != nil {
			return value{}, err
		}
		return value{kind: valBool, boolean: !v.truthy()}, nil
	}
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
	if p.src[0] == '"' {
		return p.parseStringLiteral('"')
	}
	if unicode.IsLetter(rune(p.src[0])) || p.src[0] == '_' {
		if v, ok, n := p.parseBoolLiteral(); ok {
			p.src = p.src[n:]
			return v, nil
		}
		return p.parseIdentOrCall()
	}
	return value{}, fmt.Errorf("unexpected character %q", p.src[0])
}

func (p *parser) parseBoolLiteral() (value, bool, int) {
	if strings.HasPrefix(p.src, "true") && identBoundary(p.src, 4) {
		return value{kind: valBool, boolean: true}, true, 4
	}
	if strings.HasPrefix(p.src, "false") && identBoundary(p.src, 5) {
		return value{kind: valBool, boolean: false}, true, 5
	}
	return value{}, false, 0
}

func identBoundary(s string, n int) bool {
	if len(s) <= n {
		return true
	}
	r := rune(s[n])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
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

func (p *parser) parseStringLiteral(quote byte) (value, error) {
	p.src = p.src[1:]
	var b strings.Builder
	for len(p.src) > 0 {
		if p.src[0] == quote {
			p.src = p.src[1:]
			return value{kind: valString, str: b.String()}, nil
		}
		if p.src[0] == '\\' && len(p.src) > 1 {
			p.src = p.src[1:]
			b.WriteByte(p.src[0])
			p.src = p.src[1:]
			continue
		}
		b.WriteByte(p.src[0])
		p.src = p.src[1:]
	}
	return value{}, fmt.Errorf("unterminated string")
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
	case "sin_deg", "sin":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: sinDeg(ns[0])}, nil
	case "cos_deg", "cos":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: cosDeg(ns[0])}, nil
	case "tan_deg", "tan":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: tanDeg(ns[0])}, nil
	case "asin_deg", "asin":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: asinDeg(ns[0])}, nil
	case "acos_deg", "acos":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: acosDeg(ns[0])}, nil
	case "atan_deg", "atan":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: atanDeg(ns[0])}, nil
	case "hypot":
		ns, err := nums(2)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: math.Hypot(ns[0], ns[1])}, nil
	case "sqrt":
		ns, err := nums(1)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: math.Sqrt(ns[0])}, nil
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
	case "hash01":
		ns, err := nums(2)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: hash01(ns[0], ns[1])}, nil
	case "book_thickness":
		ns, err := nums(4)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: bookThickness(ns[0], ns[1], ns[2], ns[3])}, nil
	case "book_cluster_count":
		ns, err := nums(5)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: float64(bookClusterCount(ns[0], ns[1], ns[2], ns[3], ns[4]))}, nil
	case "book_cluster_x":
		ns, err := nums(6)
		if err != nil {
			return value{}, err
		}
		return value{kind: valNumber, number: bookClusterX(ns[0], ns[1], ns[2], ns[3], ns[4], ns[5])}, nil
	case "vec3_scale":
		if len(args) != 2 {
			return value{}, fmt.Errorf("vec3_scale wants 2 args, got %d", len(args))
		}
		if args[0].kind != valVec3 {
			return value{}, fmt.Errorf("vec3_scale arg 0: expected vec3, got %s", args[0].describe())
		}
		scale, err := args[1].asNumber()
		if err != nil {
			return value{}, fmt.Errorf("vec3_scale arg 1: %w", err)
		}
		v := args[0].vec3
		return value{kind: valVec3, vec3: [3]float64{v[0] * scale, v[1] * scale, v[2] * scale}}, nil
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
