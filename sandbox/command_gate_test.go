package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/basenana/friday/core/providers"
)

func decisionProviderWith(values ...float64) *fakeDecisionProvider {
	return &fakeDecisionProvider{fn: func(context.Context, providers.DecisionRequest) (providers.DecisionResponse, error) {
		return safeDecisionResponse(values...), nil
	}}
}

func TestCommandGateStaticPermissionOrdering(t *testing.T) {
	t.Run("allow list bypasses provider", func(t *testing.T) {
		exec := approvalTestExecutor([]string{"echo"}, nil)
		provider := decisionProviderWith(1, 1, 1)
		automation, _ := NewCommandAutomation(provider, 0.6)
		gate := NewCommandGate(exec, nil, automation)
		if err := gate.Authorize(context.Background(), CommandRequest{Command: "echo ok", Mode: CommandForeground}); err != nil {
			t.Fatal(err)
		}
		if provider.calls != 0 {
			t.Fatalf("provider calls = %d, want 0", provider.calls)
		}
	})

	t.Run("explicit deny bypasses provider and approver", func(t *testing.T) {
		exec := approvalTestExecutor(nil, []string{"sudo"})
		provider := decisionProviderWith(0, 0, 0)
		automation, _ := NewCommandAutomation(provider, 0.6)
		prompter := &fakePrompter{queue: []string{approvalValueOnce}}
		approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
		approver.Bind(prompter)
		err := NewCommandGate(exec, approver, automation).Authorize(context.Background(), CommandRequest{Command: "sudo true", Mode: CommandForeground})
		var denied *DeniedError
		if !errors.As(err, &denied) || !denied.ExplicitDeny {
			t.Fatalf("error = %v, want explicit deny", err)
		}
		if provider.calls != 0 {
			t.Fatalf("provider calls = %d, want 0", provider.calls)
		}
		if calls, _ := prompter.stats(); calls != 0 {
			t.Fatalf("approval calls = %d, want 0", calls)
		}
	})

	t.Run("parse failure bypasses provider", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		provider := decisionProviderWith(0, 0, 0)
		automation, _ := NewCommandAutomation(provider, 0.6)
		if err := NewCommandGate(exec, nil, automation).Authorize(context.Background(), CommandRequest{Command: "'", Mode: CommandForeground}); err == nil {
			t.Fatal("parse failure was allowed")
		}
		if provider.calls != 0 {
			t.Fatalf("provider calls = %d, want 0", provider.calls)
		}
	})

	t.Run("unavailable sandbox bypasses provider and approver", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		exec.config.Sandbox.Enabled = true
		exec.sandbox = &unavailableTestSandbox{}
		provider := decisionProviderWith(0, 0, 0)
		automation, _ := NewCommandAutomation(provider, 0.6)
		prompter := &fakePrompter{queue: []string{approvalValueOnce}}
		approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
		approver.Bind(prompter)
		err := NewCommandGate(exec, approver, automation).Authorize(context.Background(), CommandRequest{Command: "custom", Mode: CommandForeground})
		if !errors.Is(err, ErrSandboxUnavailable) {
			t.Fatalf("error = %v, want ErrSandboxUnavailable", err)
		}
		if provider.calls != 0 {
			t.Fatalf("provider calls = %d, want 0", provider.calls)
		}
		if calls, _ := prompter.stats(); calls != 0 {
			t.Fatalf("approval calls = %d, want 0", calls)
		}
	})
}

func TestCommandGateAutomationDecisions(t *testing.T) {
	t.Run("low risk allows invocation without grant", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		provider := decisionProviderWith(0.1, 0.2, 0.3)
		automation, _ := NewCommandAutomation(provider, 0.6)
		gate := NewCommandGate(exec, nil, automation)
		if err := gate.Authorize(context.Background(), CommandRequest{Command: "custom", Mode: CommandForeground}); err != nil {
			t.Fatal(err)
		}
		if decision, _ := exec.Permission().Check("custom"); decision != Deny {
			t.Fatal("low-risk automation polluted the allow list")
		}
	})

	t.Run("high risk uses existing approval grant", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		provider := decisionProviderWith(0.1, 0.7, 0.1)
		automation, _ := NewCommandAutomation(provider, 0.6)
		prompter := &fakePrompter{queue: []string{approvalValueOnce}}
		approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
		approver.Bind(prompter)
		if err := NewCommandGate(exec, approver, automation).Authorize(context.Background(), CommandRequest{Command: "custom", Mode: CommandForeground}); err != nil {
			t.Fatal(err)
		}
		if decision, _ := exec.Permission().Check("custom"); decision != Allow {
			t.Fatal("approved command was not granted")
		}
	})

	t.Run("provider failure falls back to approval", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		provider := &fakeDecisionProvider{fn: func(context.Context, providers.DecisionRequest) (providers.DecisionResponse, error) {
			return providers.DecisionResponse{}, errors.New("offline")
		}}
		automation, _ := NewCommandAutomation(provider, 0.6)
		prompter := &fakePrompter{queue: []string{approvalValueOnce}}
		approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
		approver.Bind(prompter)
		if err := NewCommandGate(exec, approver, automation).Authorize(context.Background(), CommandRequest{Command: "custom", Mode: CommandForeground}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCommandGateCompatibilityAndCancellation(t *testing.T) {
	t.Run("automation off foreground approves", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		prompter := &fakePrompter{queue: []string{approvalValueOnce}}
		approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
		approver.Bind(prompter)
		if err := NewCommandGate(exec, approver, nil).Authorize(context.Background(), CommandRequest{Command: "custom", Mode: CommandForeground}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("automation off background denies without prompt", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		prompter := &fakePrompter{queue: []string{approvalValueOnce}}
		approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
		approver.Bind(prompter)
		err := NewCommandGate(exec, approver, nil).Authorize(context.Background(), CommandRequest{Command: "custom", Mode: CommandBackground})
		if !IsDenied(err) {
			t.Fatalf("error = %v, want denial", err)
		}
		if calls, _ := prompter.stats(); calls != 0 {
			t.Fatalf("approval calls = %d, want 0", calls)
		}
	})

	t.Run("parent cancellation never approves", func(t *testing.T) {
		exec := approvalTestExecutor(nil, nil)
		provider := &fakeDecisionProvider{fn: func(ctx context.Context, _ providers.DecisionRequest) (providers.DecisionResponse, error) {
			<-ctx.Done()
			return providers.DecisionResponse{}, ctx.Err()
		}}
		automation, _ := NewCommandAutomation(provider, 0.6)
		prompter := &fakePrompter{queue: []string{approvalValueOnce}}
		approver := NewCommandApprover(exec.Permission(), approvalOverlayPath(t))
		approver.Bind(prompter)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := NewCommandGate(exec, approver, automation).Authorize(ctx, CommandRequest{Command: "custom", Mode: CommandForeground})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if calls, _ := prompter.stats(); calls != 0 {
			t.Fatalf("approval calls = %d, want 0", calls)
		}
	})
}
