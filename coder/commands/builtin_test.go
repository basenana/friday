package commands

import (
	"strings"
	"testing"
)

func actionAt[T Action](t *testing.T, result *Result, index int) T {
	t.Helper()
	if result == nil || index < 0 || index >= len(result.Actions) {
		t.Fatalf("missing action %d in %#v", index, result)
	}
	action, ok := result.Actions[index].(T)
	if !ok {
		t.Fatalf("action %d = %T, want %T", index, result.Actions[index], *new(T))
	}
	return action
}

func TestClearCmd(t *testing.T) {
	r, err := clearCmd{}.Execute(nil)
	if err != nil {
		t.Fatalf("clear Execute error: %v", err)
	}
	if actionAt[ClearSessionAction](t, r, 0).SessionID == "" {
		t.Error("clear should create a target session ID")
	}
}

func TestQuitCmd(t *testing.T) {
	r, err := quitCmd{}.Execute(nil)
	if err != nil {
		t.Fatalf("quit Execute error: %v", err)
	}
	actionAt[QuitAction](t, r, 0)
}

func TestHelpCmd(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltins(reg)
	r, err := helpCmd{registry: reg}.Execute(nil)
	if err != nil {
		t.Fatalf("help Execute error: %v", err)
	}
	message := actionAt[AppendMessageAction](t, r, 0).Content
	if !strings.Contains(message, "Available commands") {
		t.Errorf("help message missing header; got: %q", message)
	}
	if !strings.Contains(message, "/clear") {
		t.Errorf("help message should list /clear; got: %q", message)
	}
}

func TestPlanCmd_NoArgsEntersPlanMode(t *testing.T) {
	ctx := &Context{Args: nil}
	r, err := newPlanCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("plan Execute error: %v", err)
	}
	action := actionAt[SetModeAction](t, r, 0)
	if action.Mode != "plan" || action.Prompt != "" {
		t.Fatalf("unexpected plan action: %+v", action)
	}
}

func TestPlanCmd_WithArgsEntersAndSubmits(t *testing.T) {
	ctx := &Context{Args: []string{"implement", "login"}, RawArgs: "implement login"}
	r, err := newPlanCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("plan Execute error: %v", err)
	}
	action := actionAt[SetModeAction](t, r, 0)
	if action.Mode != "plan" || action.Prompt != "implement login" {
		t.Fatalf("unexpected plan action: %+v", action)
	}
}

func TestReviewCmd_NoArgsUsesDefaultDiff(t *testing.T) {
	ctx := &Context{Args: nil}
	r, err := newReviewCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("review Execute error: %v", err)
	}
	action := actionAt[RunAgentAction](t, r, 0)
	if action.Agent != "reviewer" {
		t.Errorf("review agent = %q, want %q", action.Agent, "reviewer")
	}
	if !strings.Contains(action.Input, "git status --short") || !strings.Contains(action.Input, "untracked files") {
		t.Errorf("review with no args should inspect status and untracked files; got %q", action.Input)
	}
}

func TestAdvisorCmd_NoArgsReturnsUsage(t *testing.T) {
	ctx := &Context{Args: nil}
	r, err := newAdvisorCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("advisor Execute error: %v", err)
	}
	message := actionAt[AppendMessageAction](t, r, 0).Content
	if !strings.Contains(message, "usage") {
		t.Errorf("advisor with no args should return usage; got %q", message)
	}
}

func TestAdvisorCmd_WithArgsDelegates(t *testing.T) {
	ctx := &Context{Args: []string{"is", "this", "ok?"}}
	r, err := newAdvisorCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("advisor Execute error: %v", err)
	}
	if action := actionAt[RunAgentAction](t, r, 0); action.Agent != "advisor" {
		t.Errorf("advisor agent = %q, want %q", action.Agent, "advisor")
	}
}
