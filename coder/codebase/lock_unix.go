//go:build unix

package codebase

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
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
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("project %s is already open in another TUI: %w", projectRoot, err)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }) }, nil
}
