//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

func TestGenerateProfileDeniesNetworkWhenIsolationEnabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = true
	s := NewSeatbelt(cfg)

	profile := s.generateProfile("", "")
	if !strings.Contains(profile, "(deny network*)") {
		t.Errorf("profile = %q, want (deny network*)", profile)
	}
	if strings.Contains(profile, "(allow network-outbound)") {
		t.Errorf("profile = %q, must not allow network-outbound when isolation is enabled", profile)
	}
}

func TestGenerateProfileAllowsNetworkWhenIsolationDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Network.Isolation = false
	s := NewSeatbelt(cfg)

	profile := s.generateProfile("", "")
	if !strings.Contains(profile, "(deny network*)") {
		t.Errorf("profile = %q, want (deny network*) as the default", profile)
	}
	if !strings.Contains(profile, "(allow network-outbound)") {
		t.Errorf("profile = %q, want (allow network-outbound) when isolation is disabled", profile)
	}
}

func TestGenerateProfileDeniesConfiguredPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.Deny = []string{"/etc/secret"}
	s := NewSeatbelt(cfg)

	profile := s.generateProfile("", "")
	if !strings.Contains(profile, `(deny file-read* (subpath "/private/etc/secret"))`) {
		t.Errorf("profile = %q, want deny file-read* for /etc/secret", profile)
	}
}

func TestGenerateProfileRestrictsWritesToWorkdirAndConfiguredRoots(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.Write = []string{"/tmp/allowed"}
	s := NewSeatbelt(cfg)

	profile := s.generateProfile("/tmp/project", "")
	if !strings.Contains(profile, "(deny file-write* (require-not (require-any") ||
		!strings.Contains(profile, `(subpath "/private/tmp/project")`) ||
		!strings.Contains(profile, `(subpath "/private/tmp/allowed")`) {
		t.Fatalf("profile does not enforce write roots: %s", profile)
	}
}

func TestGenerateProfileTurnsProtectedGlobIntoRegex(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.Protected = []string{"*.pem"}
	s := NewSeatbelt(cfg)

	profile := s.generateProfile("/tmp/project", "")
	if !strings.Contains(profile, `(deny file-write* (regex #"`) || !strings.Contains(profile, `[^/]*\\.pem`) {
		t.Fatalf("profile does not protect glob paths: %s", profile)
	}
}
