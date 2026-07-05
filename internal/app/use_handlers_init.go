package app

import "log"

// invokeHandler is wired in init() so handleDocument can call other handlers
// without an initialization cycle on UseHandlers.
var invokeHandler func(string, *UseContext) error

func init() {
	UseHandlers = map[string]UseHandler{
		"exit_portal": handleExitPortal,
		"door":        handleDoor,
		"document":    handleDocument,
	}
	invokeHandler = func(name string, ctx *UseContext) error {
		if name == "" {
			return nil
		}
		h := UseHandlers[name]
		if h == nil {
			log.Printf("unknown interact handler %q", name)
			return nil
		}
		return h(ctx)
	}
}
