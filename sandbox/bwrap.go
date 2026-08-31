//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/basenana/friday/shellcmd"
)

// Bwrap implements Sandbox using Linux bubblewrap
type Bwrap struct {
	config *Config

	probeOnce sync.Once
	probeOK   bool
}

// NewBwrap creates a new Bwrap sandbox
func NewBwrap(cfg *Config) *Bwrap {
	return &Bwrap{config: cfg}
}

// WrapCommand wraps a command to run in the sandbox
func (b *Bwrap) WrapCommand(cmd string, opts ExecOptions) (string, func(), error) {
	if !b.config.Sandbox.Enabled {
		return cmd, func() {}, nil
	}

	// Build bwrap arguments
	args := b.buildArgs(opts.Workdir, opts.HomeDir)

	// Quote each argument (binary, flags, and the inner command) with bash
	// quoting rules so values containing spaces or metacharacters stay a
	// single argument.
	wrappedCmd := shellcmd.Join("bwrap", append(args, "--", "bash", "-c", cmd)...)
	cleanup := func() {}

	return wrappedCmd, cleanup, nil
}

// IsAvailable checks if bwrap is available and functional.
// The probe result is memoized: bwrap is only executed once per process.
func (b *Bwrap) IsAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		return false
	}
	b.probeOnce.Do(func() {
		// Probe bwrap to verify it can actually run (e.g. nested containers
		// may block namespace creation). The probe uses the same argument
		// builder as real invocations so it cannot diverge from them.
		cmd := exec.Command("bwrap", b.probeArgs()...)
		b.probeOK = cmd.Run() == nil
	})
	return b.probeOK
}

// probeArgs derives the availability probe arguments from buildArgs so the
// probe exercises the same mount layout as a real sandboxed command.
func (b *Bwrap) probeArgs() []string {
	return append(b.buildArgs("", ""), "--", "true")
}

// Name returns the name of this sandbox
func (b *Bwrap) Name() string {
	return "bubblewrap"
}

// buildArgs builds bubblewrap arguments.
//
// Mount ordering matters: bubblewrap applies arguments in order, so the host
// root is first mounted read-only, then writable mounts are layered on top,
// and deny paths are masked with an empty tmpfs last so they can never be
// re-exposed by a later bind.
func (b *Bwrap) buildArgs(workdir string, homeDir string) []string {
	var args []string

	// Basic isolation
	args = append(args,
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-ipc",
		"--new-session",
		"--cap-drop", "ALL",
	)

	// Host root is visible read-only; writable access is granted explicitly
	// below via the write paths and the working directory.
	args = append(args, "--ro-bind", "/", "/")

	// Proc filesystem — use bind mount as fallback when --proc is not permitted (e.g., in containers)
	if os.Getenv("FRIDAY_SANDBOX_PROC_BIND") != "" {
		args = append(args, "--bind", "/proc", "/proc")
	} else {
		args = append(args, "--proc", "/proc")
	}

	// Devtmpfs for /dev
	args = append(args, "--dev", "/dev")
	args = addLinuxRuntimeCompatMounts(args)

	// Readonly paths
	for _, path := range b.config.Sandbox.Filesystem.ReadOnly {
		expanded := expandPath(path, workdir, homeDir)
		if _, err := os.Stat(expanded); err == nil {
			args = append(args, "--ro-bind", expanded, expanded)
		}
	}

	// Write paths (rw bind mount)
	for _, path := range b.config.Sandbox.Filesystem.Write {
		expanded := expandPath(path, workdir, homeDir)
		if _, err := os.Stat(expanded); err == nil {
			args = append(args, "--bind", expanded, expanded)
		}
	}

	// Set working directory (writable)
	if workdir != "" {
		absWorkdir, err := filepath.Abs(workdir)
		if err != nil {
			absWorkdir = filepath.Clean(workdir)
		}
		args = append(args, "--bind", absWorkdir, absWorkdir)
		args = append(args, "--chdir", absWorkdir)
	}

	// Deny paths are masked with an empty tmpfs. They are applied last so a
	// deny path inside a writable mount (including the workdir) stays hidden.
	for _, path := range b.config.Sandbox.Filesystem.Deny {
		expanded := expandPath(path, workdir, homeDir)
		args = append(args, "--tmpfs", filepath.Clean(expanded))
	}

	// Network isolation
	if b.config.Sandbox.Network.Isolation {
		args = append(args, "--unshare-net")
		// If network is needed, we'd set up a proxy
		// For now, just unshare network completely
		if len(b.config.Sandbox.Network.Allow) > 0 {
			// TODO: Set up proxy for allowed domains
		}
	}

	return args
}
