//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/basenana/friday/shellcmd"
)

// Seatbelt implements Sandbox using macOS sandbox-exec (Seatbelt)
type Seatbelt struct {
	config *Config
}

// NewSeatbelt creates a new Seatbelt sandbox
func NewSeatbelt(cfg *Config) *Seatbelt {
	return &Seatbelt{config: cfg}
}

// WrapCommand wraps a command to run in the sandbox
func (s *Seatbelt) WrapCommand(cmd string, opts ExecOptions) (string, func(), error) {
	if !s.config.Sandbox.Enabled {
		return cmd, func() {}, nil
	}

	// Generate the sandbox profile
	profile := s.generateProfile(opts.Workdir, opts.HomeDir)

	// Write profile to temp file
	tmpFile, err := os.CreateTemp("", "friday-sandbox-*.sb")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp profile file: %w", err)
	}

	profilePath := tmpFile.Name()
	if _, err := tmpFile.WriteString(profile); err != nil {
		tmpFile.Close()
		os.Remove(profilePath)
		return "", nil, fmt.Errorf("failed to write profile: %w", err)
	}
	tmpFile.Close()

	// Quote each argument (binary, flags, and the inner command) with bash
	// quoting rules so values containing spaces or metacharacters stay a
	// single argument.
	wrappedCmd := shellcmd.Join("sandbox-exec", "-f", profilePath, "--", "bash", "-c", cmd)

	cleanup := func() {
		os.Remove(profilePath)
	}

	return wrappedCmd, cleanup, nil
}

// IsAvailable checks if sandbox-exec is available and functional
func (s *Seatbelt) IsAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	binary, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return false
	}
	trueBin, err := exec.LookPath("true")
	if err != nil {
		for _, candidate := range []string{"/usr/bin/true", "/bin/true"} {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				trueBin = candidate
				break
			}
		}
	}
	if trueBin == "" {
		return false
	}
	cmd := exec.Command(binary, "-p", "(version 1) (allow default)", trueBin)
	return cmd.Run() == nil
}

// Name returns the name of this sandbox
func (s *Seatbelt) Name() string {
	return "seatbelt"
}

// generateProfile generates a Seatbelt profile
func (s *Seatbelt) generateProfile(workdir string, homeDir string) string {
	var sb strings.Builder

	sb.WriteString("(version 1)\n")
	sb.WriteString("(allow default)\n")

	// Deny reading sensitive paths
	for _, path := range s.config.Sandbox.Filesystem.Deny {
		expanded := expandPath(path, workdir, homeDir)
		sb.WriteString(fmt.Sprintf("(deny file-read* (subpath %q))\n", expanded))
	}

	// Allow writing to specified paths
	for _, path := range s.config.Sandbox.Filesystem.Write {
		expanded := expandPath(path, workdir, homeDir)
		sb.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", expanded))
	}

	// Deny writing to protected paths (even if in write list)
	for _, path := range s.config.Sandbox.Filesystem.Protected {
		expanded := expandPath(path, workdir, homeDir)
		sb.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n", expanded))
	}

	// Mount readonly paths as read-only
	for _, path := range s.config.Sandbox.Filesystem.ReadOnly {
		expanded := expandPath(path, workdir, homeDir)
		sb.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n", expanded))
		sb.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n", expanded))
	}

	// Network restrictions: deny everything by default and only allow
	// outbound connections when network isolation is disabled.
	//
	// Note: Network.Allow is an allow-list enforced by the image tool for
	// URL downloads; it does not apply to shell commands executed under
	// Seatbelt, which get either full outbound access (isolation disabled)
	// or no network at all (isolation enabled).
	sb.WriteString("(deny network*)\n")
	if !s.config.Sandbox.Network.Isolation {
		sb.WriteString("(allow network-outbound)\n")
	}

	return sb.String()
}
