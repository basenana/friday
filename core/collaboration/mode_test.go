package collaboration

import (
	"context"
	"strings"
	"testing"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

type fixedModes map[string]Mode

func (m fixedModes) CollaborationMode(id string) Mode { return m[id] }

func TestHookInjectsPlanInstructionsAndEffort(t *testing.T) {
	hook := NewHook(fixedModes{"s": ModePlan}, "medium")
	req := providers.NewRequest("base")
	if err := hook.BeforeModel(context.Background(), session.New("s", nil), req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.SystemPrompt(), "decision-complete") {
		t.Fatalf("missing plan instructions: %q", req.SystemPrompt())
	}
	if providers.RequestReasoningEffort(req) != "medium" {
		t.Fatalf("effort = %q", providers.RequestReasoningEffort(req))
	}
}

func TestHookLeavesDefaultModeUntouched(t *testing.T) {
	hook := NewHook(fixedModes{"s": ModeDefault}, "medium")
	req := providers.NewRequest("base")
	if err := hook.BeforeModel(context.Background(), session.New("s", nil), req); err != nil {
		t.Fatal(err)
	}
	if req.SystemPrompt() != "base\n\n" || providers.RequestReasoningEffort(req) != "" {
		t.Fatalf("default request changed: prompt=%q effort=%q", req.SystemPrompt(), providers.RequestReasoningEffort(req))
	}
}

func TestHookFiltersCollaborationToolsByCurrentMode(t *testing.T) {
	all := []providers.ToolDefine{
		tools.NewTool(EnterPlanModeToolName),
		tools.NewTool(RequestUserInputToolName),
		tools.NewTool(SubmitPlanToolName),
		tools.NewTool("request_form"),
		tools.NewTool("write_todos"),
		tools.NewTool("bash"),
	}
	assertNames := func(t *testing.T, got []providers.ToolDefine, want ...string) {
		t.Helper()
		names := map[string]bool{}
		for _, define := range got {
			names[define.GetName()] = true
		}
		if len(names) != len(want) {
			t.Fatalf("tool names = %v, want %v", names, want)
		}
		for _, name := range want {
			if !names[name] {
				t.Fatalf("tool %q missing from %v", name, names)
			}
		}
	}

	defaultReq := providers.NewRequest("base")
	defaultReq.AppendToolDefines(all...)
	if err := NewHook(fixedModes{"s": ModeDefault}).BeforeModel(context.Background(), session.New("s", nil), defaultReq); err != nil {
		t.Fatal(err)
	}
	assertNames(t, defaultReq.ToolDefines(), EnterPlanModeToolName, "request_form", "write_todos", "bash")
	if !strings.Contains(defaultReq.SystemPrompt(), "may call enter_plan_mode") {
		t.Fatalf("missing default collaboration instructions: %q", defaultReq.SystemPrompt())
	}

	planReq := providers.NewRequest("base")
	planReq.AppendToolDefines(all...)
	if err := NewHook(fixedModes{"s": ModePlan}).BeforeModel(context.Background(), session.New("s", nil), planReq); err != nil {
		t.Fatal(err)
	}
	assertNames(t, planReq.ToolDefines(), RequestUserInputToolName, SubmitPlanToolName, "bash")
}
