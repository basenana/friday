package agents

import (
	"testing"

	"github.com/basenana/friday/config"
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

func TestExplorerSpec_HasReadOnlyDenyPolicy(t *testing.T) {
	spec := ExplorerSpec(config.ModelConfig{})
	denied := make(map[string]struct{}, len(spec.ToolPolicy.Deny))
	for _, n := range spec.ToolPolicy.Deny {
		denied[n] = struct{}{}
	}
	for _, mustDeny := range []string{ToolFsWrite, ToolFsEdit, ToolFsDelete, ToolBash} {
		if _, ok := denied[mustDeny]; !ok {
			t.Errorf("explorer policy missing deny for %q", mustDeny)
		}
	}
	if spec.MaxLoopTimes != 30 {
		t.Errorf("explorer MaxLoopTimes = %d, want 30", spec.MaxLoopTimes)
	}
	if spec.Mode != ModeSubagent {
		t.Errorf("explorer Mode = %v, want ModeSubagent", spec.Mode)
	}
}

func TestPlannerSpec_OnlyAllowsReadOnly(t *testing.T) {
	spec := PlannerSpec(config.ModelConfig{})
	allowed := make(map[string]struct{}, len(spec.ToolPolicy.Allow))
	for _, n := range spec.ToolPolicy.Allow {
		allowed[n] = struct{}{}
	}
	for _, must := range []string{ToolFsRead, ToolFsList} {
		if _, ok := allowed[must]; !ok {
			t.Errorf("planner policy missing allow for %q", must)
		}
	}
	if len(spec.ToolPolicy.Allow) != 2 {
		t.Errorf("planner should only allow 2 tools, got %d", len(spec.ToolPolicy.Allow))
	}
}

func TestReviewerSpec_DeniesWriteKeepsBash(t *testing.T) {
	spec := ReviewerSpec(config.ModelConfig{})
	denied := make(map[string]struct{}, len(spec.ToolPolicy.Deny))
	for _, n := range spec.ToolPolicy.Deny {
		denied[n] = struct{}{}
	}
	for _, mustDeny := range []string{ToolFsWrite, ToolFsEdit, ToolFsDelete} {
		if _, ok := denied[mustDeny]; !ok {
			t.Errorf("reviewer policy missing deny for %q", mustDeny)
		}
	}
	if _, ok := denied[ToolBash]; ok {
		t.Error("reviewer should NOT deny bash (needed for git diff / tests)")
	}
}

func TestAdvisorSpec_OnlyReadOnly(t *testing.T) {
	spec := AdvisorSpec(config.ModelConfig{})
	if len(spec.ToolPolicy.Allow) == 0 {
		t.Fatal("advisor should use an allow list")
	}
}
