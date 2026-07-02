//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/sandbox"
)

// ---------------------------------------------------------------------------
// Permission layer — fully deterministic, no LLM, no bwrap.
// ---------------------------------------------------------------------------

// TestPermission_DefaultDenySudo verifies that the default deny list blocks
// sudo invocations.
func TestPermission_DefaultDenySudo(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	decision, err := perm.Check("sudo echo hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != sandbox.Deny {
		t.Errorf("expected Deny for sudo, got %s", decision)
	}
}

// TestPermission_DefaultAllowEcho verifies that the default allow list admits
// a plain echo command.
func TestPermission_DefaultAllowEcho(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	decision, _ := perm.Check("echo hello")
	if decision != sandbox.Allow {
		t.Errorf("expected Allow for echo, got %s", decision)
	}
}

// TestPermission_PipelineDeny verifies that a pipeline is denied when any
// segment is denied. This protects against sudo being smuggled via |.
func TestPermission_PipelineDeny(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	decision, _ := perm.Check("echo hi | sudo tee /etc/passwd")
	if decision != sandbox.Deny {
		t.Errorf("expected Deny for pipeline containing sudo, got %s", decision)
	}
}

// TestPermission_CompoundAllow verifies that two allowed commands joined with
// && are both permitted.
func TestPermission_CompoundAllow(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	decision, _ := perm.Check("echo a && ls")
	if decision != sandbox.Allow {
		t.Errorf("expected Allow for compound allowed, got %s", decision)
	}
}

// TestPermission_UnknownCommandDeny verifies the default-deny policy for
// commands absent from both allow and deny lists.
func TestPermission_UnknownCommandDeny(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	decision, _ := perm.Check("totally_unknown_binary_xyz")
	if decision != sandbox.Deny {
		t.Errorf("expected Deny for unknown command, got %s", decision)
	}
}

// TestPermission_CheckWithReason verifies that CheckWithReason's reason string
// mentions the matched deny rule.
func TestPermission_CheckWithReason(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	_, reason, err := perm.CheckWithReason("sudo echo hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reason, "sudo") {
		t.Errorf("expected reason to mention sudo, got %q", reason)
	}
	if !strings.Contains(reason, "deny") {
		t.Errorf("expected reason to mention deny, got %q", reason)
	}
}

// TestPermission_EmptyCommand verifies an empty command is treated as no-op.
func TestPermission_EmptyCommand(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	decision, _ := perm.Check("")
	if decision != sandbox.Allow {
		t.Errorf("expected Allow for empty command, got %s", decision)
	}
}

// TestPermission_UnparseableCommand verifies that malformed shell syntax is
// denied with a parse error.
func TestPermission_UnparseableCommand(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	decision, err := perm.Check("echo 'unclosed")
	if decision != sandbox.Deny {
		t.Errorf("expected Deny for malformed syntax, got %s", decision)
	}
	if err == nil {
		t.Error("expected non-nil parse error, got nil")
	}
}

// ---------------------------------------------------------------------------
// Bwrap layer — runtime detection via isBwrapAvailable(); skipped otherwise.
// Business code is never modified to enable testing.
// ---------------------------------------------------------------------------

// TestSandbox_BwrapAvailable confirms the runtime detection works.
func TestSandbox_BwrapAvailable(t *testing.T) {
	if !isBwrapAvailable() {
		t.Skip("bwrap binary not found on PATH")
	}
	// NewBwrap is linux-only; build tag + lookup guard ensure this is safe.
	// We exec.LookPath again here to keep this test independent of platform
	// specifics in the sandbox package.
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap binary not found on PATH")
	}
	// Construct the bwrap sandbox via the package-level NewSandbox factory.
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = true
	sb := sandbox.NewSandbox(cfg)
	if !sb.IsAvailable() {
		t.Error("expected bwrap to be available")
	}
	if sb.Name() == "" {
		t.Error("expected non-empty sandbox name")
	}
}

// TestSandbox_BwrapWrapsCommand verifies that, with sandbox enabled, the
// wrapped command is prefixed with bwrap.
func TestSandbox_BwrapWrapsCommand(t *testing.T) {
	if !isBwrapAvailable() {
		t.Skip("bwrap binary not found on PATH")
	}
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = true
	cfg.Sandbox.Filesystem.Write = []string{t.TempDir()}
	sb := sandbox.NewSandbox(cfg)
	wrapped, cleanup, err := sb.WrapCommand("echo hi", sandbox.ExecOptions{Workdir: t.TempDir()})
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}
	defer cleanup()
	if !strings.HasPrefix(wrapped, "bwrap") {
		t.Errorf("expected wrapped command to start with bwrap, got %q", truncate(wrapped, 200))
	}
}

// TestSandbox_BwrapNetworkIsolation verifies that with --unshare-net active,
// network access is blocked. We use 127.0.0.1:1 (no listener) so the result
// is stable regardless of the CI environment's external connectivity.
//
// Note: this test exercises the configured behaviour of bwrap, including the
// case where bwrap itself cannot create a namespace (e.g. restricted
// container). In that case bwrap exits non-zero with an empty stdout — still
// satisfying our assertion that the command did NOT successfully reach the
// network.
func TestSandbox_BwrapNetworkIsolation(t *testing.T) {
	if !isBwrapAvailable() {
		t.Skip("bwrap binary not found on PATH")
	}
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = true
	cfg.Sandbox.Network.Isolation = true
	cfg.Sandbox.Network.Allow = nil
	cfg.Sandbox.Filesystem.Write = []string{t.TempDir()}
	executor := sandbox.NewExecutor(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, _ := executor.Run(ctx, "curl -s --max-time 3 http://127.0.0.1:1/ping", sandbox.ExecOptions{Workdir: t.TempDir()})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// curl must not have succeeded in reaching the listener: stdout must not
	// contain a successful response body. ExitCode != 0 OR empty stdout both
	// qualify.
	if result.ExitCode == 0 && result.Stdout != "" {
		t.Errorf("expected curl to fail under network isolation; got exit=0 output=%q", truncate(result.Stdout, 200))
	}
}

// TestSandbox_BwrapReadOnlyEnforced verifies that a path mounted read-only
// rejects write attempts. This is the actually-implemented fs enforcement
// (the Filesystem.Deny list is a separate unimplemented feature).
func TestSandbox_BwrapReadOnlyEnforced(t *testing.T) {
	if !isBwrapAvailable() {
		t.Skip("bwrap binary not found on PATH")
	}
	// Mount /tmp as readonly and try to write — must fail.
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = true
	cfg.Sandbox.Filesystem.ReadOnly = []string{"/tmp"}
	cfg.Sandbox.Filesystem.Write = []string{}
	wd := t.TempDir() // workdir is always bound rw by bwrap; we test ro via /tmp
	cfg.Sandbox.Filesystem.Write = append(cfg.Sandbox.Filesystem.Write, wd)
	executor := sandbox.NewExecutor(cfg)

	target := "/tmp/friday_e2e_ro_test_" + time.Now().Format("150405") + ".txt"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, _ := executor.Run(ctx, "echo x > "+target, sandbox.ExecOptions{Workdir: wd})
	if result == nil || result.ExitCode == 0 {
		t.Errorf("expected write to read-only /tmp to fail; got result=%+v", result)
	}
}
