//go:build e2e

package e2e

import (
	"errors"
	"strings"
	"testing"

	"github.com/basenana/friday/sandbox"
)

// Native Seatbelt and bubblewrap behavior is tested by sandbox's backend
// contract suite. These e2e tests cover the independent command permission
// layer and do not require an OS sandbox backend.

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

// TestPermission_CheckWithReason verifies that CheckWithReason's denial error
// mentions the matched deny rule and is marked explicit.
func TestPermission_CheckWithReason(t *testing.T) {
	perm := sandbox.NewPermission(sandbox.DefaultConfig())
	_, err := perm.CheckWithReason("sudo echo hi")
	if err == nil {
		t.Fatal("expected denial error")
	}
	var denied *sandbox.DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("expected *sandbox.DeniedError, got %T", err)
	}
	if !strings.Contains(denied.Reason, "sudo") {
		t.Errorf("expected reason to mention sudo, got %q", denied.Reason)
	}
	if !strings.Contains(denied.Reason, "deny rule") {
		t.Errorf("expected reason to mention deny rule, got %q", denied.Reason)
	}
	if !denied.ExplicitDeny {
		t.Error("expected ExplicitDeny for deny-rule match")
	}
	if !sandbox.IsDenied(err) {
		t.Error("denial error should wrap ErrPermissionDenied")
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
