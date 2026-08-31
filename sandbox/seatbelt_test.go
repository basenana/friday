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
	if !strings.Contains(profile, `(deny file-read* (subpath "/etc/secret"))`) {
		t.Errorf("profile = %q, want deny file-read* for /etc/secret", profile)
	}
}
