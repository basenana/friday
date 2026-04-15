package anthropics

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
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

func TestMessageCreateParamsTurnsPromptOnlyRequestIntoUserMessage(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("summarize this conversation")

	params := cli.messageCreateParams(req)
	if len(params.System) != 0 {
		t.Fatalf("expected prompt-only request to avoid system-only payload, got %#v", params.System)
	}
	if len(params.Messages) != 1 {
		t.Fatalf("expected one synthesized user message, got %d", len(params.Messages))
	}
	if params.Messages[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected synthesized message role=user, got %q", params.Messages[0].Role)
	}
	if len(params.Messages[0].Content) != 1 {
		t.Fatalf("expected synthesized user message to contain one text block, got %#v", params.Messages[0].Content)
	}
	if got := params.Messages[0].Content[0].GetText(); got == nil || *got != "summarize this conversation" {
		t.Fatalf("expected synthesized user message to preserve prompt text, got %#v", params.Messages[0].Content[0])
	}
}

func TestMessageCreateParamsDowngradesInvalidHistoricalToolCallAndResultToText(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I inspected the file.",
			ToolCalls: []types.ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: `{"path":"core/session/compact.go"...`,
			}},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-1", Content: "file content"},
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 2 {
		t.Fatalf("expected assistant and user messages, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	if len(assistant.Content) < 2 {
		t.Fatalf("expected assistant message to preserve text and include fallback note, got %#v", assistant.Content)
	}
	if got := assistant.Content[0].GetText(); got == nil || *got != "I inspected the file." {
		t.Fatalf("expected assistant text to be preserved, got %#v", assistant.Content[0])
	}
	if assistant.Content[1].OfToolUse != nil {
		t.Fatalf("expected invalid tool call to be downgraded to text, got %#v", assistant.Content[1])
	}
	if got := assistant.Content[1].GetText(); got == nil || !strings.Contains(*got, "invalid tool call") {
		t.Fatalf("expected fallback note for invalid tool call, got %#v", assistant.Content[1])
	}

	toolResult := params.Messages[1]
	if len(toolResult.Content) != 1 {
		t.Fatalf("expected one downgraded tool result block, got %#v", toolResult.Content)
	}
	if toolResult.Content[0].OfToolResult != nil {
		t.Fatalf("expected tool result referencing invalid call to be downgraded to text, got %#v", toolResult.Content[0])
	}
	if got := toolResult.Content[0].GetText(); got == nil || !strings.Contains(*got, "file content") {
		t.Fatalf("expected downgraded tool result text to preserve content, got %#v", toolResult.Content[0])
	}
}
