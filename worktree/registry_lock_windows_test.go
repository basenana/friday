//go:build windows

package worktree

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryLockSerializesGoroutinesOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worktrees", ".lock")
	var active atomic.Int32
	var overlapped atomic.Bool
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := withRegistryLock(path, func() error {
				if active.Add(1) != 1 {
					overlapped.Store(true)
				}
				time.Sleep(time.Millisecond)
				active.Add(-1)
				return nil
			}); err != nil {
				t.Errorf("withRegistryLock: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if overlapped.Load() {
		t.Fatal("Windows registry lock allowed overlapping in-process critical sections")
	}
}
