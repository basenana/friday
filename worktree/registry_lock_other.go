//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package worktree

func withRegistryLock(_ string, fn func() error) error { return fn() }
