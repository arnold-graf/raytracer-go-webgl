package sceneparam

import (
	"strings"
	"testing"
)

func TestIfStringComparison(t *testing.T) {
	raw := []byte(`
[props]
primitive = "cylinder"

[[box]]
# if primitive == "cylinder"
marker = 1
# endif
# if primitive is "box"
marker = 2
# endif
`)
	out, err := Expand("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "marker = 1") {
		t.Fatalf("expected cylinder branch:\n%s", out)
	}
	if strings.Contains(string(out), "marker = 2") {
		t.Fatalf("did not expect box branch:\n%s", out)
	}
}

func TestIfNumericComparison(t *testing.T) {
	raw := []byte(`
[props]
steps = 10

[[box]]
# if steps >= 10
wide = true
# endif
# if steps < 5
narrow = true
# endif
`)
	out, err := Expand("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "wide = true") {
		t.Fatalf("expected wide branch:\n%s", out)
	}
	if strings.Contains(s, "narrow = true") {
		t.Fatalf("did not expect narrow branch:\n%s", out)
	}
}

func TestIfNotExpression(t *testing.T) {
	raw := []byte(`
[props]
on = false

[[box]]
# if not on
off = true
# endif
`)
	out, err := Expand("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "off = true") {
		t.Fatalf("expected not on branch:\n%s", out)
	}
}

func TestIfInForLoop(t *testing.T) {
	raw := []byte(`
[props]
kind = "a"

# for i in range(2)
# if kind == "a"
[[box]]
pos_x = 'i'
# endif
# endfor
`)
	out, err := Expand("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "[[box]]") != 2 {
		t.Fatalf("expected two boxes:\n%s", out)
	}
}

func TestCompareExpressions(t *testing.T) {
	env := NewEnv()
	env.Set("x", value{kind: valNumber, number: 3})
	tests := []struct {
		expr string
		want bool
	}{
		{`x == 3`, true},
		{`x != 3`, false},
		{`x < 5`, true},
		{`x > 5`, false},
		{`x >= 3`, true},
		{`x <= 2`, false},
		{`"a" is "a"`, true},
		{`"a" is not "b"`, true},
	}
	for _, tc := range tests {
		v, err := evalExpr(tc.expr, env)
		if err != nil {
			t.Fatalf("%q: %v", tc.expr, err)
		}
		if v.kind != valBool || v.boolean != tc.want {
			t.Fatalf("%q = %v, want %v", tc.expr, v.boolean, tc.want)
		}
	}
}
