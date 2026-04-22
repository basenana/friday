//go:build linux

package sandbox

import (
	"testing"
)

func TestBuildArgsProcMount(t *testing.T) {
	cfg := DefaultConfig()
	b := NewBwrap(cfg)

	// Default: --proc /proc
	args := b.buildArgs("/tmp")
	if !containsSeq(args, "--proc", "/proc") {
		t.Errorf("expected --proc /proc in args, got %v", args)
	}

	// With FRIDAY_SANDBOX_PROC_BIND: --bind /proc /proc
	t.Setenv("FRIDAY_SANDBOX_PROC_BIND", "1")
	args = b.buildArgs("/tmp")
	if !containsSeq(args, "--bind", "/proc", "/proc") {
		t.Errorf("expected --bind /proc /proc in args, got %v", args)
	}
}

// containsSeq checks if seq appears as a contiguous subsequence in args
func containsSeq(args []string, seq ...string) bool {
	for i := 0; i <= len(args)-len(seq); i++ {
		match := true
		for j, s := range seq {
			if args[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
