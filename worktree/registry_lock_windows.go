//go:build windows

package worktree

import (
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

var registryProcessLocks sync.Map

// withRegistryLock serializes both goroutines in this process and independent
// Friday processes. LockFileEx alone must not be used as the goroutine mutex:
// Windows byte-range lock semantics for multiple handles owned by one process
// are not a portable substitute for in-process exclusion.
func withRegistryLock(path string, fn func() error) error {
	path = filepath.Clean(path)
	value, _ := registryProcessLocks.LoadOrStore(path, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return err
	}
	defer windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, overlapped)
	return fn()
}
