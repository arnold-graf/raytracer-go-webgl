package scene

import "strings"

// IsStateAction reports whether on_use is a reactive state mutation expression.
func IsStateAction(onUse string) bool {
	s := strings.TrimSpace(onUse)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "toggle(") && strings.HasSuffix(s, ")") {
		return true
	}
	return strings.Contains(s, "=")
}

// InteractFromOnUse builds an interactable for TOML on_use values.
func InteractFromOnUse(hint, onUse string, rng float64) Interactable {
	ia := Interactable{Hint: hint, Range: rng}
	if onUse == "" {
		return ia
	}
	if IsStateAction(onUse) {
		ia.Handler = "state"
		ia.StateAction = onUse
		return ia
	}
	ia.Handler = onUse
	return ia
}
