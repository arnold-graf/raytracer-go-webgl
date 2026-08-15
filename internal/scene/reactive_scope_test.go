package scene

import "testing"

func TestIncludeScopePrefix(t *testing.T) {
	got := IncludeScopePrefix("/scenes/office-sunset/server-room-front-office.toml", 4)
	want := "server-room-front-office.toml#4/"
	if got != want {
		t.Fatalf("prefix = %q, want %q", got, want)
	}
}

func TestPrefixFragmentScopes(t *testing.T) {
	frag := ReactiveFragment{
		ScopeID:       "desk-anglepoise-lamp.toml",
		ParentScopeID: "server-room-front-office.toml",
	}
	PrefixFragmentScopes(&frag, "server-room-front-office.toml#4/front-office-desk.toml#3/")
	if frag.ScopeID != "server-room-front-office.toml#4/front-office-desk.toml#3/desk-anglepoise-lamp.toml" {
		t.Fatalf("ScopeID = %q", frag.ScopeID)
	}
	if frag.ParentScopeID != "server-room-front-office.toml#4/front-office-desk.toml#3/server-room-front-office.toml" {
		t.Fatalf("ParentScopeID = %q", frag.ParentScopeID)
	}
}

func TestPrefixFragmentScopesSkipsScopeForBoundFragment(t *testing.T) {
	frag := ReactiveFragment{
		SourcePath:    "/scenes/objects/light-switch.toml",
		ParentScopeID: "server-room-front-office.toml",
	}
	PrefixFragmentScopes(&frag, "index.toml#1/")
	if frag.ScopeID != "" {
		t.Fatalf("ScopeID = %q, want empty for bound fragment", frag.ScopeID)
	}
	if frag.ParentScopeID != "index.toml#1/server-room-front-office.toml" {
		t.Fatalf("ParentScopeID = %q", frag.ParentScopeID)
	}
}
