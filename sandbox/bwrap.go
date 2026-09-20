//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/basenana/friday/shellcmd"
)

// Bwrap implements Sandbox using Linux bubblewrap.
type Bwrap struct {
	config *Config
	binary string

	probeOnce sync.Once
	probeOK   bool
}

func NewBwrap(cfg *Config) *Bwrap {
	binary, _ := exec.LookPath("bwrap")
	if binary != "" {
		if absolute, err := filepath.Abs(binary); err == nil {
			binary = absolute
		}
		if physical, err := filepath.EvalSymlinks(binary); err == nil {
			binary = physical
		}
	}
	return &Bwrap{config: cfg, binary: binary}
}

func (b *Bwrap) WrapCommand(cmd string, opts ExecOptions) (string, func(), error) {
	if !b.config.Sandbox.Enabled {
		return cmd, func() {}, nil
	}
	if b.binary == "" {
		return "", nil, fmt.Errorf("bubblewrap is unavailable")
	}
	args, cleanup, err := b.buildArgs(opts.Workdir, opts.HomeDir)
	if err != nil {
		return "", nil, err
	}
	return shellcmd.Join(b.binary, append(args, "--", "bash", "-c", cmd)...), cleanup, nil
}

// IsAvailable executes the production mount/namespace builder once. This
// catches kernels that expose bwrap but prohibit user namespaces.
func (b *Bwrap) IsAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if b.binary == "" {
		return false
	}
	b.probeOnce.Do(func() {
		workdir, err := os.MkdirTemp("", "friday-bwrap-probe-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(workdir)
		args, cleanup, err := b.buildArgs(workdir, workdir)
		if err != nil {
			return
		}
		defer cleanup()
		b.probeOK = exec.Command(b.binary, append(args, "--", "true")...).Run() == nil
	})
	return b.probeOK
}

func (b *Bwrap) Name() string { return "bubblewrap" }

// buildArgs compiles the shared filesystem policy and renders it in mount
// precedence order. Its cleanup owns temporary regular-file deny masks.
func (b *Bwrap) buildArgs(workdir, homeDir string) ([]string, func(), error) {
	policy, err := compileFilesystemPolicy(b.config, workdir, homeDir)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {}
	args := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-ipc",
		"--new-session",
		"--cap-drop", "ALL",
		"--ro-bind", "/", "/",
	}
	if os.Getenv("FRIDAY_SANDBOX_PROC_BIND") != "" {
		args = append(args, "--bind", "/proc", "/proc")
	} else {
		args = append(args, "--proc", "/proc")
	}
	args = append(args,
		"--tmpfs", "/dev",
		"--dev-bind", "/dev/null", "/dev/null",
	)
	for _, device := range []string{"/dev/zero", "/dev/random", "/dev/urandom"} {
		args = append(args, "--ro-bind", device, device)
	}
	args = append(args,
		"--symlink", "/proc/self/fd", "/dev/fd",
		"--symlink", "fd/0", "/dev/stdin",
		"--symlink", "fd/1", "/dev/stdout",
		"--symlink", "fd/2", "/dev/stderr",
	)

	// Writable roots, including workdir, must be mounted before narrower
	// readonly/protected overlays.
	for _, rule := range policy.Rules {
		if rule.Kind == filesystemRuleWrite {
			args = append(args, "--bind", rule.Path, rule.Path)
		}
	}
	for _, rule := range policy.Rules {
		if rule.Kind == filesystemRuleReadOnly || rule.Kind == filesystemRuleProtected {
			args = append(args, "--ro-bind", rule.Path, rule.Path)
		}
	}

	var maskDir string
	for _, rule := range policy.Rules {
		if rule.Kind != filesystemRuleDeny {
			continue
		}
		switch rule.Object {
		case filesystemObjectDirectory:
			args = append(args, "--perms", "0555", "--tmpfs", rule.Path, "--remount-ro", rule.Path)
		case filesystemObjectFile:
			if maskDir == "" {
				maskDir, err = os.MkdirTemp("", "friday-bwrap-deny-*")
				if err != nil {
					return nil, nil, fmt.Errorf("create deny mask directory: %w", err)
				}
				cleanup = func() { _ = os.RemoveAll(maskDir) }
			}
			mask, err := os.CreateTemp(maskDir, "mask-*")
			if err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("create deny file mask: %w", err)
			}
			maskPath := mask.Name()
			if err := mask.Close(); err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("close deny file mask: %w", err)
			}
			if err := os.Chmod(maskPath, 0o444); err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("make deny file mask readonly: %w", err)
			}
			args = append(args, "--ro-bind", maskPath, rule.Path)
		default:
			cleanup()
			return nil, nil, fmt.Errorf("unsupported deny object %q", rule.Path)
		}
	}

	args = append(args, "--chdir", policy.Workdir)
	if b.config.Sandbox.Network.Isolation {
		args = append(args, "--unshare-net")
	}
	return args, cleanup, nil
}
