//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Bwrap implements Sandbox using Linux bubblewrap
type Bwrap struct {
	config *Config
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
	args := b.buildArgs(opts.Workdir)

	// Safely quote the command for bash -c using syntax.Quote
	quotedCmd, err := syntax.Quote(cmd, syntax.LangBash)
	if err != nil {
		// Fallback to basic escaping if Quote fails
		quotedCmd = "'" + strings.ReplaceAll(cmd, "'", "'\\''") + "'"
	}
	wrappedCmd := fmt.Sprintf("bwrap %s -- bash -c %s", strings.Join(args, " "), quotedCmd)
	cleanup := func() {}

	return wrappedCmd, cleanup, nil
}

// IsAvailable checks if bwrap is available
func (b *Bwrap) IsAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("bwrap")
	return err == nil
}

// Name returns the name of this sandbox
func (b *Bwrap) Name() string {
	return "bubblewrap"
}

// buildArgs builds bubblewrap arguments
func (b *Bwrap) buildArgs(workdir string) []string {
	var args []string

	// Basic isolation
	args = append(args,
		//"--unshare-pid",
		"--die-with-parent",
	)

	// Proc filesystem
	args = append(args, "--proc", "/proc")

	// Devtmpfs for /dev
	args = append(args, "--dev", "/dev")

	// Mount necessary system directories as read-only
	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin", "/sbin"} {
		if _, err := os.Stat(dir); err == nil {
			args = append(args, "--ro-bind", dir, dir)
		}
	}

	// Readonly paths
	for _, path := range b.config.Sandbox.Filesystem.ReadOnly {
		expanded := expandPath(path, workdir)
		if _, err := os.Stat(expanded); err == nil {
			args = append(args, "--ro-bind", expanded, expanded)
		}
	}

	// Write paths (rw bind mount)
	for _, path := range b.config.Sandbox.Filesystem.Write {
		expanded := expandPath(path, workdir)
		if _, err := os.Stat(expanded); err == nil {
			args = append(args, "--bind", expanded, expanded)
		}
	}

	// Set working directory
	if workdir != "" {
		absWorkdir, _ := filepath.Abs(workdir)
		args = append(args, "--bind", absWorkdir, absWorkdir)
		args = append(args, "--chdir", absWorkdir)
	}

	// Network isolation
	if b.config.Sandbox.Network.Enabled {
		args = append(args, "--unshare-net")
		// If network is needed, we'd set up a proxy
		// For now, just unshare network completely
		if len(b.config.Sandbox.Network.Allow) > 0 {
			// TODO: Set up proxy for allowed domains
		}
	}

	return args
}
