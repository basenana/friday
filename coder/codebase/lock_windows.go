//go:build windows

package codebase

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

func AcquireProjectLock(dataDir, projectID, projectRoot string) (func(), error) {
	if projectID == "" || projectRoot == "" {
		return nil, fmt.Errorf("project identity is required")
	}
	path := filepath.Join(dataDir, "projects", projectID, ".tui-owner.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	o := &windows.Overlapped{}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, o); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("project %s is already open in another TUI: %w", projectRoot, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, o); _ = f.Close() })
	}, nil
}
