//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func containsSeq(args []string, seq ...string) bool {
	return seqIndex(args, seq...) >= 0
}

func seqIndex(args []string, seq ...string) int {
	for i := 0; i <= len(args)-len(seq); i++ {
		match := true
		for j, value := range seq {
			if args[i+j] != value {
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

func mustBuildArgs(t *testing.T, b *Bwrap, workdir string) []string {
	t.Helper()
	args, cleanup, err := b.buildArgs(workdir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return args
}

func TestBuildArgsProcMount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	workdir := t.TempDir()
	b := NewBwrap(cfg)

	if args := mustBuildArgs(t, b, workdir); !containsSeq(args, "--proc", "/proc") {
		t.Errorf("expected --proc /proc in args, got %v", args)
	}

	t.Setenv("FRIDAY_SANDBOX_PROC_BIND", "1")
	if args := mustBuildArgs(t, b, workdir); !containsSeq(args, "--bind", "/proc", "/proc") {
		t.Errorf("expected --bind /proc /proc in args, got %v", args)
	}
}

func TestBuildArgsLayersWriteBeforeNestedReadOnlyAndProtected(t *testing.T) {
	workdir := t.TempDir()
	readonly := filepath.Join(workdir, "readonly")
	protected := filepath.Join(workdir, "secret.pem")
	if err := os.Mkdir(readonly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{
		ReadOnly:  []string{readonly},
		Protected: []string{"*.pem"},
	}
	args := mustBuildArgs(t, NewBwrap(cfg), workdir)

	writeIndex := seqIndex(args, "--bind", workdir, workdir)
	readOnlyIndex := seqIndex(args, "--ro-bind", readonly, readonly)
	protectedIndex := seqIndex(args, "--ro-bind", protected, protected)
	if writeIndex < 0 || readOnlyIndex < 0 || protectedIndex < 0 {
		t.Fatalf("missing expected mounts: %v", args)
	}
	if writeIndex > readOnlyIndex || writeIndex > protectedIndex {
		t.Fatalf("writable parent must precede readonly overlays: %v", args)
	}
	if containsSeq(args, "--ro-bind", filepath.Join(workdir, "*.pem"), filepath.Join(workdir, "*.pem")) {
		t.Fatalf("glob must be expanded to concrete objects: %v", args)
	}
}

func TestBuildArgsMasksDenyDirectoryAndFile(t *testing.T) {
	workdir := t.TempDir()
	deniedDir := filepath.Join(workdir, "denied-dir")
	deniedFile := filepath.Join(workdir, "denied-file")
	if err := os.Mkdir(deniedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deniedFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Deny: []string{deniedDir, deniedFile}}
	args := mustBuildArgs(t, NewBwrap(cfg), workdir)

	dirIndex := seqIndex(args, "--perms", "0555", "--tmpfs", deniedDir, "--remount-ro", deniedDir)
	if dirIndex < 0 {
		t.Fatalf("deny directory must use a readonly empty tmpfs: %v", args)
	}
	fileIndex := -1
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--ro-bind" && args[i+2] == deniedFile && args[i+1] != deniedFile {
			fileIndex = i
			info, err := os.Stat(args[i+1])
			if err != nil {
				t.Fatalf("stat deny mask: %v", err)
			}
			if !info.Mode().IsRegular() || info.Size() != 0 || info.Mode().Perm()&0o222 != 0 {
				t.Fatalf("deny mask must be an empty readonly regular file: mode=%s size=%d", info.Mode(), info.Size())
			}
		}
	}
	if fileIndex < 0 {
		t.Fatalf("deny file must use a readonly regular-file mask: %v", args)
	}
	if writeIndex := seqIndex(args, "--bind", workdir, workdir); writeIndex > dirIndex || writeIndex > fileIndex {
		t.Fatalf("deny masks must follow writable mounts: %v", args)
	}
}

func TestBuildArgsCanonicalizesSymlinkRules(t *testing.T) {
	workdir := t.TempDir()
	target := filepath.Join(workdir, "target")
	alias := filepath.Join(workdir, "alias")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Protected: []string{alias}}
	args := mustBuildArgs(t, NewBwrap(cfg), workdir)
	if !containsSeq(args, "--ro-bind", target, target) {
		t.Fatalf("expected canonical target mount, got %v", args)
	}
}

func TestBuildArgsDropsCapsAndIsolatesNamespaces(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	args := mustBuildArgs(t, NewBwrap(cfg), t.TempDir())
	for _, seq := range [][]string{
		{"--cap-drop", "ALL"}, {"--unshare-pid"}, {"--unshare-ipc"}, {"--new-session"}, {"--die-with-parent"},
		{"--tmpfs", "/dev"}, {"--dev-bind", "/dev/null", "/dev/null"},
		{"--symlink", "/proc/self/fd", "/dev/fd"}, {"--symlink", "fd/0", "/dev/stdin"},
		{"--symlink", "fd/1", "/dev/stdout"}, {"--symlink", "fd/2", "/dev/stderr"},
	} {
		if !containsSeq(args, seq...) {
			t.Errorf("expected %v in args, got %v", seq, args)
		}
	}
	if containsSeq(args, "--dev", "/dev") {
		t.Fatalf("private /dev must not create extra devices: %v", args)
	}
	for _, device := range []string{"/dev/zero", "/dev/random", "/dev/urandom"} {
		if !containsSeq(args, "--ro-bind", device, device) {
			t.Errorf("device %s is not read-only: %v", device, args)
		}
	}
}

func TestWrapCommandUsesProbedBinaryPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	b := NewBwrap(cfg)
	b.binary = "/trusted/bwrap"
	workdir := t.TempDir()
	wrapped, cleanup, err := b.WrapCommand("true", ExecOptions{Workdir: workdir, HomeDir: workdir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if !strings.HasPrefix(wrapped, "/trusted/bwrap ") {
		t.Fatalf("wrapped command did not pin backend path: %q", wrapped)
	}
}

func TestBuildArgsNetworkIsolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	workdir := t.TempDir()
	cfg.Sandbox.Network.Isolation = true
	b := NewBwrap(cfg)
	if !containsSeq(mustBuildArgs(t, b, workdir), "--unshare-net") {
		t.Error("expected --unshare-net")
	}
	cfg.Sandbox.Network.Isolation = false
	if containsSeq(mustBuildArgs(t, b, workdir), "--unshare-net") {
		t.Error("expected no --unshare-net")
	}
}
