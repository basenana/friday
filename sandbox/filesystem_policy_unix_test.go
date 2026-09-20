//go:build unix

package sandbox

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCompileFilesystemPolicyRejectsDenyDevice(t *testing.T) {
	workdir := canonicalTestDir(t)
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Deny: []string{os.DevNull}}
	if _, err := compileFilesystemPolicy(cfg, workdir, workdir); err == nil {
		t.Fatal("expected deny device to fail closed")
	}
}

func TestCompileFilesystemPolicyRejectsUnsupportedSpecialObject(t *testing.T) {
	workdir := canonicalTestDir(t)
	fifo := filepath.Join(workdir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Protected: []string{fifo}}
	if _, err := compileFilesystemPolicy(cfg, workdir, workdir); err == nil {
		t.Fatal("expected FIFO policy rule to fail closed")
	}
}
