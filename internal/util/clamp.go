package util

import "math"

// ContactsAt samples the set of contact ids at a scalar parameter value.
// Ids are opaque to the clamp; callers compare sets for new entries.
type ContactsAt func(value float64) map[int]bool

// ClampNewContacts returns the furthest value between current and proposed
// that does not introduce contacts beyond those already present at current.
// Useful when existing touch contacts (e.g. closed door against a jamb) should
// not block further motion, but new overlaps with static geometry should.
func ClampNewContacts(current, proposed float64, at ContactsAt) float64 {
	if !hasNewContacts(at(current), at(proposed)) {
		return proposed
	}
	lo, hi := current, proposed
	if math.Abs(hi-lo) < 1e-6 {
		return lo
	}
	for i := 0; i < 12; i++ {
		mid := (lo + hi) / 2
		if hasNewContacts(at(current), at(mid)) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return lo
}

func hasNewContacts(current, proposed map[int]bool) bool {
	for idx := range proposed {
		if !current[idx] {
			return true
		}
	}
	return false
}
