package agents

import (
	"strings"
	"testing"

	"git.hypo.xyz/agents/friday/core/providers"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	spec := &AgentSpec{Name: "x", Description: "test"}
	reg.Register(spec)

	got, ok := reg.Get("x")
	if !ok {
		t.Fatal("Get returned not-found after Register")
	}
	if got.Name != "x" {
		t.Errorf("got name %q want %q", got.Name, "x")
	}
}

func TestRegistry_GetMissingReturnsFalse(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.Get("nope"); ok {
		t.Fatal("Get should return false for unregistered name")
	}
}

func TestRegistry_RegisterNilOrEmptyIgnored(t *testing.T) {
	reg := NewRegistry()
	reg.Register(nil)
	reg.Register(&AgentSpec{Name: ""})
	if len(reg.List()) != 0 {
		t.Fatalf("nil or empty-name specs should be ignored, got %d", len(reg.List()))
	}
}

func TestRegistry_ListReturnsAllRegistered(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&AgentSpec{Name: "a"})
	reg.Register(&AgentSpec{Name: "b"})
	if len(reg.List()) != 2 {
		t.Fatalf("List returned %d, want 2", len(reg.List()))
	}
}

func TestSideCheckerSpec_HasReadOnlyDenyPolicy(t *testing.T) {
	spec := SideCheckerSpec()
	if spec.Name != NameSideChecker {
		t.Fatalf("side-checker name = %q, want %q", spec.Name, NameSideChecker)
	}
	denied := make(map[string]struct{}, len(spec.ToolPolicy.Deny))
	for _, n := range spec.ToolPolicy.Deny {
		denied[n] = struct{}{}
	}
	for _, mustDeny := range []string{ToolFsWrite, ToolFsEdit, ToolFsDelete, ToolBash} {
		if _, ok := denied[mustDeny]; !ok {
			t.Errorf("side-checker policy missing deny for %q", mustDeny)
		}
	}
	if spec.MaxLoopTimes != 50 {
		t.Errorf("side-checker MaxLoopTimes = %d, want 50", spec.MaxLoopTimes)
	}
	if spec.Mode != ModeSubagent {
		t.Errorf("side-checker Mode = %v, want ModeSubagent", spec.Mode)
	}
	if spec.Effort != providers.ReasoningEffortNone {
		t.Errorf("side-checker Effort = %q, want none", spec.Effort)
	}
	for _, phrase := range []string{"focused, read-only question", "conversation and project context", "Stop as soon as", "## Files Examined"} {
		if !strings.Contains(spec.SystemPrompt, phrase) {
			t.Errorf("side-checker prompt missing %q", phrase)
		}
	}
}
