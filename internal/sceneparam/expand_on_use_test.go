package sceneparam

import (
	"os"
	"testing"
)

func TestExpandDeskLampToggleOnUse(t *testing.T) {
	raw, err := os.ReadFile("../../scenes/objects/desk-anglepoise-lamp.toml")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = ExpandWithReactive("desk-anglepoise-lamp.toml", raw, nil, "desk-anglepoise-lamp.toml", nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpandStatePanelToggleOnUse(t *testing.T) {
	raw, err := os.ReadFile("../../scenes/objects/state-panel.toml")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = ExpandWithReactive("state-panel.toml", raw, nil, "state-panel.toml", nil)
	if err != nil {
		t.Fatal(err)
	}
}
