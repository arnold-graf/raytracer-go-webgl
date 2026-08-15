package sceneparam

import (
	"testing"
)

func TestIncludeStatePropDeps(t *testing.T) {
	deps, err := IncludeStatePropDeps("../../scenes/office-sunset/server-room-front-office.toml", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0].Prop != "on" || deps[0].StateKey != "is_ceiling_light_on" {
		t.Fatalf("light switch deps = %+v", deps)
	}

	deps, err = IncludeStatePropDeps("../../scenes/office-sunset/server-room-front-office.toml", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0].Prop != "on" || deps[0].StateKey != "is_ceiling_light_on" {
		t.Fatalf("grid[0] deps = %+v", deps)
	}

	deps, err = IncludeStatePropDeps("../../scenes/office-sunset/server-room-front-office.toml", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0].Prop != "on" || deps[0].StateKey != "is_ceiling_light_on" {
		t.Fatalf("grid[1] deps = %+v", deps)
	}
}
