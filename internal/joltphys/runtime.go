package joltphys

import (
	"fmt"
	"sync"

	"github.com/bbitechnologies/jolt-go/jolt"
)

var (
	initOnce sync.Once
	initErr  error
	inited   bool
)

// Init starts the Jolt runtime (safe to call multiple times).
func Init() error {
	initOnce.Do(func() {
		initErr = jolt.Init()
		inited = initErr == nil
	})
	return initErr
}

// Shutdown releases the Jolt runtime. Call once at process exit after all Worlds
// are destroyed.
func Shutdown() {
	if inited {
		jolt.Shutdown()
		inited = false
	}
}

func requireInit() error {
	if err := Init(); err != nil {
		return err
	}
	if !inited {
		return fmt.Errorf("jolt: not initialized")
	}
	return nil
}
