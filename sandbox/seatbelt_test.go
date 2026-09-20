//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustGenerateProfile(t *testing.T, s *Seatbelt, workdir string) string {
	t.Helper()
	profile, err := s.generateProfile(workdir, "")
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestGenerateProfileIsDenyByDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	profile := mustGenerateProfile(t, NewSeatbelt(cfg), t.TempDir())
	if !strings.Contains(profile, "(deny default)") || strings.Contains(profile, "(allow default)") {
		t.Fatalf("profile is not deny-by-default: %s", profile)
	}
	for _, rule := range []string{
		"(allow process-exec)",
		"(allow process-fork)",
		"(allow signal (target same-sandbox))",
		"(allow process-info* (target same-sandbox))",
		`(sysctl-name "hw.pagesize_compat")`,
	} {
		if !strings.Contains(profile, rule) {
			t.Errorf("missing %s", rule)
		}
	}
}

func TestGenerateProfileNetworkSwitch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	workdir := t.TempDir()
	cfg.Sandbox.Network.Isolation = true
	isolated := mustGenerateProfile(t, NewSeatbelt(cfg), workdir)
	if strings.Contains(isolated, "(allow network-outbound") || strings.Contains(isolated, "(allow network-inbound") {
		t.Fatalf("isolated profile grants IP network access: %s", isolated)
	}

	cfg.Sandbox.Network.Isolation = false
	connected := mustGenerateProfile(t, NewSeatbelt(cfg), workdir)
	for _, operation := range []string{"network-outbound", "network-inbound"} {
		if !strings.Contains(connected, "(allow "+operation+")") {
			t.Errorf("network-enabled profile missing %s: %s", operation, connected)
		}
	}
	if strings.Contains(connected, "(deny network*)") {
		t.Fatalf("network-enabled profile retains blanket deny: %s", connected)
	}
}

func TestGenerateProfileUsesCompiledFilesystemRules(t *testing.T) {
	workdir := t.TempDir()
	denied := filepath.Join(workdir, "secret")
	protected := filepath.Join(workdir, "one.pem")
	if err := os.WriteFile(denied, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Deny: []string{denied}, Protected: []string{"*.pem", "missing-*.key"}}
	profile := mustGenerateProfile(t, NewSeatbelt(cfg), workdir)

	for _, rule := range []string{
		`(deny file-read* file-write* (subpath "` + denied + `"))`,
		`(deny file-write* (subpath "` + protected + `"))`,
	} {
		if !strings.Contains(profile, rule) {
			t.Errorf("missing concrete rule %s in %s", rule, profile)
		}
	}
	if strings.Contains(profile, "*.pem") || strings.Contains(profile, "missing-*.key") {
		t.Fatalf("profile contains unexpanded glob: %s", profile)
	}
}

func TestGenerateProfileRestrictsWritesToCompiledRoots(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "project")
	writeRoot := filepath.Join(root, "allowed")
	if err := os.Mkdir(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(writeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Write: []string{writeRoot}}
	profile := mustGenerateProfile(t, NewSeatbelt(cfg), workdir)
	for _, root := range []string{workdir, writeRoot} {
		if !strings.Contains(profile, `(allow file-write* (subpath "`+root+`"))`) {
			t.Errorf("profile missing write root %q: %s", root, profile)
		}
	}
}

func TestGenerateProfileUsesMinimalDeviceRules(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	profile := mustGenerateProfile(t, NewSeatbelt(cfg), t.TempDir())
	for _, device := range []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/fd/0", "/dev/fd/1", "/dev/fd/2"} {
		if !strings.Contains(profile, device) {
			t.Errorf("profile missing standard device %s", device)
		}
	}
	if strings.Contains(profile, `(allow file-read* (subpath "/dev"))`) || strings.Contains(profile, `(allow file-write* (subpath "/dev"))`) {
		t.Fatalf("profile opens all of /dev: %s", profile)
	}
	if strings.Contains(profile, "/dev/tty") || strings.Contains(profile, "/dev/ptmx") {
		t.Fatalf("profile unexpectedly grants TTY access: %s", profile)
	}
}

func TestSeatbeltWrapCommandUsesProbedBinaryPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	s := NewSeatbelt(cfg)
	s.binary = "/trusted/sandbox-exec"
	workdir := t.TempDir()
	wrapped, cleanup, err := s.WrapCommand("true", ExecOptions{Workdir: workdir, HomeDir: workdir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if !strings.HasPrefix(wrapped, "/trusted/sandbox-exec ") {
		t.Fatalf("wrapped command did not pin backend path: %q", wrapped)
	}
}

func TestSeatbeltProductionProfileProbe(t *testing.T) {
	probe, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		if os.Getenv("FRIDAY_REQUIRE_NATIVE_SANDBOX") == "1" {
			t.Fatalf("outer sandbox blocks native Seatbelt test: %v", err)
		}
		t.Skipf("outer sandbox blocks /dev/null: %v", err)
	}
	probe.Close()
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		if os.Getenv("FRIDAY_REQUIRE_NATIVE_SANDBOX") == "1" {
			t.Fatal("sandbox-exec is unavailable")
		}
		t.Skip("sandbox-exec is unavailable")
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	s := NewSeatbelt(cfg)
	if !s.IsAvailable() {
		workdir := t.TempDir()
		profile, profileErr := s.generateProfile(workdir, workdir)
		if profileErr != nil {
			t.Fatalf("generate production Seatbelt profile: %v", profileErr)
		}
		trueBinary := findTrueBinary()
		output, probeErr := exec.Command(s.binary, "-p", profile, "--", trueBinary).CombinedOutput()
		minimalOutput, minimalErr := exec.Command(s.binary, "-p", "(version 1)(allow default)", "--", trueBinary).CombinedOutput()
		if minimalErr != nil && strings.Contains(string(minimalOutput), "sandbox_apply: Operation not permitted") && os.Getenv("FRIDAY_REQUIRE_NATIVE_SANDBOX") != "1" {
			t.Skipf("outer sandbox prevents nested Seatbelt: %v: %s", minimalErr, minimalOutput)
		}
		t.Fatalf("production Seatbelt profile probe failed: %v: %s; minimal nested probe: %v: %s", probeErr, output, minimalErr, minimalOutput)
	}
}
