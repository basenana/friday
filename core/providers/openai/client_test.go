package openai

import (
	"encoding/json"
	"testing"

	openaisdk "github.com/openai/openai-go"

	"github.com/basenana/friday/core/types"
)

func TestAssistantMessageParamIncludesContentToolCallsAndReasoning(t *testing.T) {
	msg := types.Message{
		Role:      types.RoleAssistant,
		Content:   "I will inspect the file.",
		Reasoning: "Need to read the implementation first.",
		ToolCalls: []types.ToolCall{{
			ID:        "call-1",
			Name:      "read_file",
			Arguments: `{"path":"core/session/compact.go"}`,
		}},
	}

	raw, err := json.Marshal(assistantMessageParam(msg))
	if err != nil {
		t.Fatalf("failed to marshal assistant message param: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal assistant message param: %v", err)
	}

	if decoded["content"] != "I will inspect the file." {
		t.Fatalf("expected content to be preserved, got %#v", decoded["content"])
	}
	if decoded["reasoning_content"] != "Need to read the implementation first." {
		t.Fatalf("expected reasoning_content to be preserved, got %#v", decoded["reasoning_content"])
	}
	toolCalls, ok := decoded["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", decoded["tool_calls"])
	}
}

func TestResponseNextChoiceEmitsContentAndToolCallsFromSameChunk(t *testing.T) {
	resp := newResponse()
	resp.nextChoice(openaisdk.ChatCompletionChunkChoice{
		Delta: openaisdk.ChatCompletionChunkChoiceDelta{
			Content: "I will inspect the file.",
			ToolCalls: []openaisdk.ChatCompletionChunkChoiceDeltaToolCall{{
				Index: 0,
				ID:    "call-1",
				Function: openaisdk.ChatCompletionChunkChoiceDeltaToolCallFunction{
					Name:      "read_file",
					Arguments: `{"path":"core/session/compact.go"}`,
				},
			}},
		},
	})
	resp.close()

	var deltas []struct {
		Content string
		ToolUse []types.ToolCall
	}
	for delta := range resp.Message() {
		item := struct {
			Content string
			ToolUse []types.ToolCall
		}{
			Content: delta.Content,
		}
		for _, tool := range delta.ToolUse {
			item.ToolUse = append(item.ToolUse, types.ToolCall{
				ID:        tool.ID,
				Name:      tool.Name,
				Arguments: tool.Arguments,
			})
		}
		deltas = append(deltas, item)
	}

	if len(deltas) != 2 {
		t.Fatalf("expected content and tool-use deltas, got %#v", deltas)
	}
	if deltas[0].Content != "I will inspect the file." {
		t.Fatalf("expected first delta to be content, got %#v", deltas[0])
	}
	if len(deltas[1].ToolUse) != 1 || deltas[1].ToolUse[0].Name != "read_file" {
		t.Fatalf("expected second delta to be tool use, got %#v", deltas[1])
	}
}
