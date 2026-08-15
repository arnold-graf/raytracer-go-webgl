package sceneparam

import (
	"strings"
	"testing"
)

func TestTernaryExpr(t *testing.T) {
	env := NewEnv()
	env.Set("on", value{kind: valBool, boolean: true})
	env.Set("base_brightness", value{kind: valNumber, number: 0.08})

	v, err := EvalNumber("on ? base_brightness : 0", env)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0.08 {
		t.Fatalf("true branch = %v, want 0.08", v)
	}

	env.Set("on", value{kind: valBool, boolean: false})
	v, err = EvalNumber("on ? base_brightness : 0", env)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Fatalf("false branch = %v, want 0", v)
	}
}

func TestExpandWithReactiveStateLamp(t *testing.T) {
	raw := []byte(`
[props]
base_brightness = 0.08

[state]
lamp_on = true

[[light]]
interactive = true
on_use = 'toggle(lamp_on)'
brightness = 'lamp_on ? base_brightness : 0'
color = [1.0, 1.0, 1.0]
pos = [0.0, 1.0, 0.0]
`)
	out, _, meta, err := ExpandWithReactive("state-lamp.toml", raw, nil, "state-lamp.toml", nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("expected reactive meta")
	}
	if len(meta.Bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(meta.Bindings))
	}
	if len(meta.Actions) != 1 || meta.Actions[0].OnUse != "toggle(lamp_on)" {
		t.Fatalf("actions = %+v", meta.Actions)
	}
	if !strings.Contains(string(out), "brightness = 0.08") {
		t.Fatalf("expanded brightness missing initial value:\n%s", out)
	}
}
