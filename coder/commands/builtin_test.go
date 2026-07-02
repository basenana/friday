package commands

import (
	"strings"
	"testing"
)

func TestClearCmd(t *testing.T) {
	r, err := clearCmd{}.Execute(nil)
	if err != nil {
		t.Fatalf("clear Execute error: %v", err)
	}
	if !r.ClearMessages {
		t.Error("clear should set ClearMessages=true")
	}
}

func TestQuitCmd(t *testing.T) {
	r, err := quitCmd{}.Execute(nil)
	if err != nil {
		t.Fatalf("quit Execute error: %v", err)
	}
	if !r.Quit {
		t.Error("quit should set Quit=true")
	}
}

func TestNewCmd(t *testing.T) {
	r, err := newCmd{}.Execute(nil)
	if err != nil {
		t.Fatalf("new Execute error: %v", err)
	}
	if r.SwitchSession == "" {
		t.Error("new should set SwitchSession to a new ID")
	}
}

func TestHelpCmd(t *testing.T) {
	reg := NewRegistry()
	RegisterBuiltins(reg)
	r, err := helpCmd{registry: reg}.Execute(nil)
	if err != nil {
		t.Fatalf("help Execute error: %v", err)
	}
	if !strings.Contains(r.Message, "Available commands") {
		t.Errorf("help message missing header; got: %q", r.Message)
	}
	if !strings.Contains(r.Message, "/clear") {
		t.Errorf("help message should list /clear; got: %q", r.Message)
	}
}

func TestPlanCmd_NoArgsReturnsUsage(t *testing.T) {
	ctx := &Context{Args: nil}
	r, err := newPlanCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("plan Execute error: %v", err)
	}
	if r.RunAgent != "" {
		t.Errorf("plan with no args should not set RunAgent; got %q", r.RunAgent)
	}
	if !strings.Contains(r.Message, "usage") {
		t.Errorf("plan with no args should return usage message; got %q", r.Message)
	}
}

func TestPlanCmd_WithArgsDelegates(t *testing.T) {
	ctx := &Context{Args: []string{"implement", "login"}}
	r, err := newPlanCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("plan Execute error: %v", err)
	}
	if r.RunAgent != "planner" {
		t.Errorf("plan RunAgent = %q, want %q", r.RunAgent, "planner")
	}
	if !strings.Contains(r.AgentInput, "implement") {
		t.Errorf("plan AgentInput should contain args; got %q", r.AgentInput)
	}
}

func TestReviewCmd_NoArgsUsesDefaultDiff(t *testing.T) {
	ctx := &Context{Args: nil}
	r, err := newReviewCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("review Execute error: %v", err)
	}
	if r.RunAgent != "reviewer" {
		t.Errorf("review RunAgent = %q, want %q", r.RunAgent, "reviewer")
	}
	if !strings.Contains(r.AgentInput, "git status --short") || !strings.Contains(r.AgentInput, "untracked files") {
		t.Errorf("review with no args should inspect status and untracked files; got %q", r.AgentInput)
	}
}

func TestAdvisorCmd_NoArgsReturnsUsage(t *testing.T) {
	ctx := &Context{Args: nil}
	r, err := newAdvisorCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("advisor Execute error: %v", err)
	}
	if !strings.Contains(r.Message, "usage") {
		t.Errorf("advisor with no args should return usage; got %q", r.Message)
	}
}

func TestAdvisorCmd_WithArgsDelegates(t *testing.T) {
	ctx := &Context{Args: []string{"is", "this", "ok?"}}
	r, err := newAdvisorCmd().Execute(ctx)
	if err != nil {
		t.Fatalf("advisor Execute error: %v", err)
	}
	if r.RunAgent != "advisor" {
		t.Errorf("advisor RunAgent = %q, want %q", r.RunAgent, "advisor")
	}
}
