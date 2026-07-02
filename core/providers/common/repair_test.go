package common

import (
	"testing"

	"github.com/basenana/friday/core/types"
)

func TestRepairToolHistory_RoleAgentToUser(t *testing.T) {
	in := []types.Message{
		{Role: types.RoleUser, Content: "hi"},
		{Role: types.RoleAgent, Content: "system nudge"},
	}
	out := RepairToolHistory(in)
	if out[1].Role != types.RoleUser {
		t.Fatalf("expected RoleAgent to be rewritten to RoleUser, got %#v", out[1])
	}
}

func TestRepairToolHistory_EmptyCallIDDowngrades(t *testing.T) {
	in := []types.Message{
		{Role: types.RoleUser, Content: "do thing"},
		{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "", Content: "orphan output"},
		},
	}
	out := RepairToolHistory(in)
	if out[1].Role != types.RoleUser || out[1].ToolResult != nil {
		t.Fatalf("expected empty-CallID tool result to become a text user message, got %#v", out[1])
	}
	if out[1].Content == "" {
		t.Fatalf("expected fallback content to be populated")
	}
}

func TestRepairToolHistory_MergesConsecutiveAssistantText(t *testing.T) {
	in := []types.Message{
		{Role: types.RoleUser, Content: "q"},
		{Role: types.RoleAssistant, Content: "first"},
		{Role: types.RoleAssistant, Content: "second"},
		{Role: types.RoleAssistant, Content: "third"},
	}
	out := RepairToolHistory(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages after merge, got %d: %#v", len(out), out)
	}
	merged := out[1]
	if merged.Role != types.RoleAssistant {
		t.Fatalf("expected merged msg to be assistant, got %#v", merged)
	}
	if merged.Content != "first\nsecond\nthird" {
		t.Fatalf("unexpected merged content: %q", merged.Content)
	}
}

func TestRepairToolHistory_DoesNotMergeWithToolCalls(t *testing.T) {
	in := []types.Message{
		{Role: types.RoleUser, Content: "q"},
		{Role: types.RoleAssistant, Content: "first", ToolCalls: []types.ToolCall{{ID: "c1", Name: "t"}}},
		{Role: types.RoleAssistant, Content: "second"},
	}
	out := RepairToolHistory(in)
	// Tool-call assistant message should not merge with the plain one.
	if len(out) != 3 {
		t.Fatalf("expected no merge, got %d messages: %#v", len(out), out)
	}
}

func TestRepairToolHistory_EmptyInput(t *testing.T) {
	if got := RepairToolHistory(nil); len(got) != 0 {
		t.Fatalf("expected nil/empty pass-through, got %#v", got)
	}
}
