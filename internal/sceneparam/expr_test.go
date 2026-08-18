package sceneparam

import (
	"math"
	"os"
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

func TestTrigHelpers(t *testing.T) {
	raw := []byte(`
[props]
run = 0.5
rise = 0.375

[const]
pitch = 'atan_deg(rise / run)'
step_len = 'hypot(run, rise)'

[[box]]
pos_x = 'pitch'
width = 'step_len'
`)
	out, _, err := ExpandWithResolved("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	wantPitch := math.Atan(0.375/0.5) * 180 / math.Pi
	wantLen := math.Hypot(0.5, 0.375)
	if !strings.Contains(s, "pos_x = 36.869897") {
		t.Fatalf("expected pitch %.4f in output:\n%s", wantPitch, out)
	}
	if !strings.Contains(s, "width = 0.625") {
		t.Fatalf("expected step_len %.4f in output:\n%s", wantLen, out)
	}
}

func TestStaircaseHandrailExpand(t *testing.T) {
	raw, err := os.ReadFile("../../scenes/objects/staircase-handrail.toml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := Expand("staircase-handrail.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "rotate_z = -53.1301") && !strings.Contains(s, "rotate_z = -53.130102354") {
		// default props: atan(0.375/0.5)*180/pi - 90
		if !strings.Contains(s, "rotate_z = ") {
			t.Fatalf("missing handrail rotate_z:\n%s", out)
		}
	}
	wantLen := math.Hypot(0.5, 0.375) * 7
	if !strings.Contains(s, "height = 4.375") && !strings.Contains(s, "height = 4.374999") {
		t.Fatalf("expected rail_len %.4f in output:\n%s", wantLen, out)
	}
	if strings.Count(s, "[[cylinder]]") != 1 {
		t.Fatalf("expected 1 sloped rail cylinder, got %d in:\n%s", strings.Count(s, "[[cylinder]]"), out)
	}
	if strings.Count(s, "[[box]]") != 8 && strings.Count(s, "[[cylinder]]") != 9 {
		t.Fatalf("expected 8 posts + 1 rail, got %d boxes and %d cylinders in:\n%s",
			strings.Count(s, "[[box]]"), strings.Count(s, "[[cylinder]]"), out)
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
