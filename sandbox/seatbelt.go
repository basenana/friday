//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

	// Keep host reads available for normal developer tooling, but make writes
	// allow-list based. A single conditional deny avoids relying on rule order:
	// protected/read-only rules below can still narrow these roots further.
	writeRoots := make([]string, 0, len(s.config.Sandbox.Filesystem.Write)+1)
	if strings.TrimSpace(workdir) != "" {
		writeRoots = append(writeRoots, canonicalSandboxPath(expandPath(workdir, workdir, homeDir)))
	}
	for _, path := range s.config.Sandbox.Filesystem.Write {
		writeRoots = append(writeRoots, canonicalSandboxPath(expandPath(path, workdir, homeDir)))
	}
	if len(writeRoots) == 0 {
		sb.WriteString("(deny file-write*)\n")
	} else {
		sb.WriteString("(deny file-write* (require-not (require-any")
		for _, root := range writeRoots {
			sb.WriteString(fmt.Sprintf(" (subpath %q)", filepath.Clean(root)))
		}
		sb.WriteString(")))\n")
	}

	// Deny reading sensitive paths
	for _, path := range s.config.Sandbox.Filesystem.Deny {
		expanded := canonicalSandboxPath(expandPath(path, workdir, homeDir))
		writeSeatbeltPathRule(&sb, "deny", "file-read*", expanded)
	}

	// Deny writing to protected paths (even if in write list)
	for _, path := range s.config.Sandbox.Filesystem.Protected {
		expanded := canonicalSandboxPath(expandPath(path, workdir, homeDir))
		writeSeatbeltPathRule(&sb, "deny", "file-write*", expanded)
	}

	// Mount readonly paths as read-only
	for _, path := range s.config.Sandbox.Filesystem.ReadOnly {
		expanded := canonicalSandboxPath(expandPath(path, workdir, homeDir))
		writeSeatbeltPathRule(&sb, "allow", "file-read*", expanded)
		writeSeatbeltPathRule(&sb, "deny", "file-write*", expanded)
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

func canonicalSandboxPath(path string) string {
	if strings.ContainsAny(path, "*?[]") {
		return canonicalizePolicyPattern(path)
	}
	if resolved, err := resolveSymlinkedPath(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func writeSeatbeltPathRule(sb *strings.Builder, decision, operation, path string) {
	if !strings.ContainsAny(path, "*?[]") {
		sb.WriteString(fmt.Sprintf("(%s %s (subpath %q))\n", decision, operation, path))
		return
	}
	pattern := regexp.QuoteMeta(filepath.ToSlash(path))
	pattern = strings.ReplaceAll(pattern, `\*`, `[^/]*`)
	pattern = strings.ReplaceAll(pattern, `\?`, `[^/]`)
	sb.WriteString(fmt.Sprintf("(%s %s (regex #%q))\n", decision, operation, "^"+pattern+"$"))
}
