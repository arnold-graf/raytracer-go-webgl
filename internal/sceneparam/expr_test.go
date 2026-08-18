package sceneparam

import (
	"strings"
	"testing"
)

func TestVec3ScaleConst(t *testing.T) {
	raw := []byte(`
[props]
case_albedo = [2.2, 2.2, 2]

[const]
key_albedo = 'vec3_scale(case_albedo, 0.5)'

[[box]]
albedo = 'key_albedo'
`)
	out, _, err := ExpandWithResolved("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "albedo = [1.1, 1.1") {
		t.Fatalf("expanded:\n%s", out)
	}
}

func TestNotBooleanNegation(t *testing.T) {
	raw := []byte(`
[const]
is_night = false

[[box]]
show_sun = '!is_night'
`)
	out, _, err := ExpandWithResolved("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "show_sun = true") {
		t.Fatalf("expected !is_night when is_night=false, got:\n%s", out)
	}

	raw = []byte(`
[const]
is_night = true

[[box]]
show_sun = '!is_night'
`)
	out, _, err = ExpandWithResolved("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "show_sun = false") {
		t.Fatalf("expected !is_night when is_night=true, got:\n%s", out)
	}
}

func TestNotBooleanNegationLiterals(t *testing.T) {
	raw := []byte(`
[const]

[[box]]
a = '!true'
b = '!false'
c = '!0'
d = '!1'
`)
	out, _, err := ExpandWithResolved("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"a = false", "b = true", "c = true", "d = false"} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected %q in:\n%s", want, out)
		}
	}
}

func TestVec3ScaleFromIncludeProps(t *testing.T) {
	raw := []byte(`
[props]
case_albedo = [2.0, 2.0, 2.0]

[const]
key_albedo = 'vec3_scale(case_albedo, 0.5)'

[[box]]
albedo = 'key_albedo'
`)
	out, _, err := ExpandWithResolved("test.toml", raw, map[string]any{
		"case_albedo": []float64{4.0, 4.0, 4.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "albedo = [2, 2, 2]") {
		t.Fatalf("expanded:\n%s", out)
	}
}
