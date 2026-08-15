package scenestate

import (
	"fmt"
	"regexp"
	"strings"

	"raytracer/internal/sceneparam"
)

var reToggle = regexp.MustCompile(`^toggle\(\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\)$`)

// Action mutates reactive state when the player uses an interactable.
type Action struct {
	ScopeID string
	Kind    string // "toggle" or "assign"
	Key     string // local state key
	Expr    string // assign rhs for Kind=="assign"
}

// ParseAction parses on_use strings like toggle(lamp_on) or lamp_on = true.
func ParseAction(scopeID, src string) (Action, error) {
	src = strings.TrimSpace(src)
	if m := reToggle.FindStringSubmatch(src); m != nil {
		return Action{ScopeID: scopeID, Kind: "toggle", Key: m[1]}, nil
	}
	eq := strings.Index(src, "=")
	if eq > 0 {
		key := strings.TrimSpace(src[:eq])
		rhs := strings.TrimSpace(src[eq+1:])
		if key == "" || rhs == "" {
			return Action{}, fmt.Errorf("invalid assignment %q", src)
		}
		return Action{ScopeID: scopeID, Kind: "assign", Key: key, Expr: rhs}, nil
	}
	return Action{}, fmt.Errorf("unsupported on_use action %q", src)
}

func (a Action) scopedKey() string {
	if a.ScopeID == "" {
		return a.Key
	}
	return a.ScopeID + "." + a.Key
}

func (a Action) run(store *Store) error {
	switch a.Kind {
	case "toggle":
		return store.Toggle(a.scopedKey())
	case "assign":
		v, err := sceneparam.EvalExpr(a.Expr, store.Env())
		if err != nil {
			return fmt.Errorf("on_use assign: %w", err)
		}
		store.SetValue(a.scopedKey(), v)
		return nil
	default:
		return fmt.Errorf("unknown action kind %q", a.Kind)
	}
}
