package sceneparam_test

import (
	"os"
	"strings"
	"testing"

	"raytracer/internal/sceneparam"
)

func TestNestedForLoops(t *testing.T) {
	raw := []byte(`
# for i in range(4)
# for j in range(4)
[[box]]
material = "emit"
pos_x = '2 + (i * 2)'
pos_y = 4.4
pos_z = '1 + (j * 2)'
width = 1
height = 0.01
depth = 1
# endfor
# endfor
`)
	out, err := sceneparam.Expand("test.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "# for") {
		t.Fatalf("unexpanded directives:\n%s", s)
	}
	n := strings.Count(s, `material = "emit"`)
	if n != 16 {
		t.Fatalf("emit boxes = %d, want 16\n%s", n, s)
	}
}

func TestOfficeLightGridExpand(t *testing.T) {
	raw, err := os.ReadFile("../../scenes/objects/office-light-grid.toml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := sceneparam.Expand("office-light-grid.toml", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "# for") {
		t.Fatalf("unexpanded # for in office light grid")
	}
	emit := strings.Count(s, `material = "emit"`)
	if emit != 12 {
		t.Fatalf("emit boxes = %d, want 12", emit)
	}
	lights := strings.Count(s, "[[light]]")
	if lights != 4 {
		t.Fatalf("lights = %d, want 4", lights)
	}
	for _, want := range []string{"2.5", "6.5", "8.5"} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected light position component %q in expanded output", want)
		}
	}
	if strings.Contains(s, "pos = [4,") || strings.Contains(s, "pos = [7,") {
		t.Fatalf("light positions should align with panel centers, not gaps")
	}
}
