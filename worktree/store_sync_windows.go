//go:build windows

package worktree

// Windows does not expose a portable directory fsync operation. Atomic file
// replacement still uses a flushed file and MoveFileEx-backed rename there.
func syncDirectory(string) error { return nil }
