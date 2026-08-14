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
