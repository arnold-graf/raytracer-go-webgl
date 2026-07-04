package util

import "testing"

func TestClampNewContacts(t *testing.T) {
	// blocked for v > 0.5
	at := func(v float64) map[int]bool {
		if v > 0.5 {
			return map[int]bool{1: true}
		}
		return nil
	}
	if got := ClampNewContacts(0, 1, at); got < 0.49 || got > 0.51 {
		t.Fatalf("ClampNewContacts = %v, want ~0.5", got)
	}
	if got := ClampNewContacts(0, 0.3, at); got != 0.3 {
		t.Fatalf("ClampNewContacts unobstructed = %v, want 0.3", got)
	}
}

func TestClampNewContactsAllowsExistingTouch(t *testing.T) {
	// contact 1 exists at current; contact 2 appears only past 0.7
	at := func(v float64) map[int]bool {
		set := map[int]bool{1: true}
		if v > 0.7 {
			set[2] = true
		}
		return set
	}
	if got := ClampNewContacts(0.2, 1, at); got < 0.69 || got > 0.71 {
		t.Fatalf("ClampNewContacts = %v, want ~0.7", got)
	}
}
