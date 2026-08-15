package sceneparam

import (
	"os"
	"testing"
)

func TestExpandIncludePropsPreservesToggle(t *testing.T) {
	raw, err := os.ReadFile("../../scenes/office-sunset/server-room-front-office.toml")
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, err := ExpandWithReactive("server-room-front-office.toml", raw, nil, "server-room-front-office.toml", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !contains(s, "toggle(is_ceiling_light_on)") {
		t.Fatalf("expected toggle preserved in output:\n%s", out)
	}
	if contains(s, "on = 'is_ceiling_light_on'") {
		t.Fatalf("expected state key evaluated in include props:\n%s", out)
	}
}

func TestExpandIncludePropsResolvesPropRefs(t *testing.T) {
	raw, err := os.ReadFile("../../scenes/office-sunset/objects/art-deco-door-frame.toml")
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, err := ExpandWithReactive("art-deco-door-frame.toml", raw, map[string]any{
		"width": 4.0, "height": 4.5, "depth": 1.2,
	}, "art-deco-door-frame.toml", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if contains(s, "steps = 'steps'") {
		t.Fatalf("expected steps prop ref resolved, got literal 'steps':\n%s", out)
	}
	if !contains(s, "steps = 5") {
		t.Fatalf("expected steps = 5 in include props:\n%s", out)
	}
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && (s == sub || stringIndex(s, sub) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
