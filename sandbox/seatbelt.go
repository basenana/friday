//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/syntax"
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
	profile := s.generateProfile(opts.Workdir)

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

	// Safely quote the command for bash -c using syntax.Quote
	quotedCmd, err := syntax.Quote(cmd, syntax.LangBash)
	if err != nil {
		// Fallback to basic escaping if Quote fails
		quotedCmd = "'" + strings.ReplaceAll(cmd, "'", "'\\''") + "'"
	}
	wrappedCmd := fmt.Sprintf("sandbox-exec -f %s -- bash -c %s", profilePath, quotedCmd)

	cleanup := func() {
		os.Remove(profilePath)
	}

	return wrappedCmd, cleanup, nil
}

// IsAvailable checks if sandbox-exec is available
func (s *Seatbelt) IsAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := exec.LookPath("sandbox-exec")
	return err == nil
}

// Name returns the name of this sandbox
func (s *Seatbelt) Name() string {
	return "seatbelt"
}

// generateProfile generates a Seatbelt profile
func (s *Seatbelt) generateProfile(workdir string) string {
	var sb strings.Builder

	sb.WriteString("(version 1)\n")
	sb.WriteString("(allow default)\n")

	// Deny reading sensitive paths
	for _, path := range s.config.Sandbox.Filesystem.Deny {
		expanded := expandPath(path, workdir)
		sb.WriteString(fmt.Sprintf("(deny file-read* (subpath %q))\n", expanded))
	}

	// Allow writing to specified paths
	for _, path := range s.config.Sandbox.Filesystem.Write {
		expanded := expandPath(path, workdir)
		sb.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", expanded))
	}

	// Deny writing to protected paths (even if in write list)
	for _, path := range s.config.Sandbox.Filesystem.Protected {
		expanded := expandPath(path, workdir)
		sb.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n", expanded))
	}

	// Mount readonly paths as read-only
	for _, path := range s.config.Sandbox.Filesystem.ReadOnly {
		expanded := expandPath(path, workdir)
		sb.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n", expanded))
		sb.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))\n", expanded))
	}

	// Network restrictions - deny by default for security
	sb.WriteString("(deny network*)\n")
	if s.config.Sandbox.Network.Isolation {
		// Allow network outbound if explicitly enabled
		sb.WriteString("(allow network-outbound)\n")
	}

	return sb.String()
}
