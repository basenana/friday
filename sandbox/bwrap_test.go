//go:build linux

package sandbox

import (
	"strings"
	"testing"
)

func TestBuildArgsProcMount(t *testing.T) {
	cfg := DefaultConfig()
	b := NewBwrap(cfg)

	// Default: --proc /proc
	args := b.buildArgs("/tmp", "")
	if !containsSeq(args, "--proc", "/proc") {
		t.Errorf("expected --proc /proc in args, got %v", args)
	}

	// With FRIDAY_SANDBOX_PROC_BIND: --bind /proc /proc
	t.Setenv("FRIDAY_SANDBOX_PROC_BIND", "1")
	args = b.buildArgs("/tmp", "")
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

// seqIndex returns the index of the first occurrence of seq in args, or -1.
func seqIndex(args []string, seq ...string) int {
	for i := 0; i <= len(args)-len(seq); i++ {
		match := true
		for j, s := range seq {
			if args[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestBuildArgsRootReadOnlyWorkdirWritable(t *testing.T) {
	cfg := DefaultConfig()
	b := NewBwrap(cfg)
	workdir := t.TempDir()

	args := b.buildArgs(workdir, "")

	if !containsSeq(args, "--ro-bind", "/", "/") {
		t.Errorf("expected --ro-bind / / in args, got %v", args)
	}
	if !containsSeq(args, "--bind", workdir, workdir) {
		t.Errorf("expected rw --bind %s %s in args, got %v", workdir, workdir, args)
	}
	if !containsSeq(args, "--chdir", workdir) {
		t.Errorf("expected --chdir %s in args, got %v", workdir, args)
	}

	// The host root must be mounted before the writable workdir so the
	// workdir bind can layer on top of it.
	rootIdx := seqIndex(args, "--ro-bind", "/", "/")
	workdirIdx := seqIndex(args, "--bind", workdir, workdir)
	if rootIdx > workdirIdx {
		t.Errorf("expected --ro-bind / / (index %d) before workdir bind (index %d)", rootIdx, workdirIdx)
	}
}

func TestBuildArgsMasksDenyPathsWithTmpfs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.Deny = append(cfg.Sandbox.Filesystem.Deny, "/etc/verysecret")
	b := NewBwrap(cfg)
	workdir := t.TempDir()

	args := b.buildArgs(workdir, "")

	if !containsSeq(args, "--tmpfs", "/etc/verysecret") {
		t.Errorf("expected --tmpfs /etc/verysecret in args, got %v", args)
	}
	// Default deny paths (e.g. ~/.ssh) must be masked too.
	foundSsh := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--tmpfs" && strings.HasSuffix(args[i+1], "/.ssh") {
			foundSsh = true
		}
	}
	if !foundSsh {
		t.Errorf("expected default deny path ~/.ssh to be masked with --tmpfs, got %v", args)
	}

	// Deny mounts must come after the workdir bind so a deny path inside the
	// workdir cannot be re-exposed.
	workdirIdx := seqIndex(args, "--bind", workdir, workdir)
	denyIdx := seqIndex(args, "--tmpfs", "/etc/verysecret")
	if workdirIdx > denyIdx {
		t.Errorf("expected workdir bind (index %d) before deny tmpfs (index %d)", workdirIdx, denyIdx)
	}
}

func TestBuildArgsDropsCapsAndIsolatesNamespaces(t *testing.T) {
	cfg := DefaultConfig()
	b := NewBwrap(cfg)

	args := b.buildArgs("", "")

	for _, seq := range [][]string{
		{"--cap-drop", "ALL"},
		{"--unshare-pid"},
		{"--unshare-ipc"},
		{"--new-session"},
	} {
		if !containsSeq(args, seq...) {
			t.Errorf("expected %v in args, got %v", seq, args)
		}
	}
}

func TestBuildArgsNetworkIsolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = true
	b := NewBwrap(cfg)
	if !containsSeq(b.buildArgs("", ""), "--unshare-net") {
		t.Errorf("expected --unshare-net in args")
	}

	cfg.Sandbox.Network.Isolation = false
	if containsSeq(b.buildArgs("", ""), "--unshare-net") {
		t.Errorf("expected no --unshare-net in args")
	}
}

func TestProbeArgsMatchBuildArgs(t *testing.T) {
	cfg := DefaultConfig()
	b := NewBwrap(cfg)

	want := append(b.buildArgs("", ""), "--", "true")
	got := b.probeArgs()

	if len(got) != len(want) {
		t.Fatalf("probeArgs length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("probeArgs[%d] = %q, want %q; got=%#v", i, got[i], want[i], got)
		}
	}
}
