//go:build !unix && !windows

package codebase

import "fmt"

func AcquireProjectLock(_, _, _ string) (func(), error) {
	return nil, fmt.Errorf("project TUI ownership locking is unsupported on this platform")
}
