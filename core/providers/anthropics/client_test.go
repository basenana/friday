package anthropics

import (
	"testing"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

func TestMessageCreateParamsKeepsMixedAssistantTextAndToolUseInSingleMessage(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("", types.Message{
		Role:    types.RoleAssistant,
		Content: "I will inspect the file.",
		ToolCalls: []types.ToolCall{{
			ID:        "call-1",
			Name:      "read_file",
			Arguments: `{"path":"core/session/compact.go"}`,
		}},
	})

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 1 {
		t.Fatalf("expected one assistant message, got %d", len(params.Messages))
	}

	msg := params.Messages[0]
	if len(msg.Content) != 2 {
		t.Fatalf("expected text and tool_use blocks in one message, got %#v", msg.Content)
	}
	if got := msg.Content[0].GetText(); got == nil || *got != "I will inspect the file." {
		t.Fatalf("expected first block to be assistant text, got %#v", msg.Content[0])
	}
	if msg.Content[1].OfToolUse == nil {
		t.Fatalf("expected second block to be tool_use, got %#v", msg.Content[1])
	}
	if msg.Content[1].OfToolUse.Name != "read_file" {
		t.Fatalf("expected tool_use block name to be preserved, got %#v", msg.Content[1].OfToolUse)
	}
}
