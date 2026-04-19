package openai

import (
	"encoding/json"
	"testing"

	"github.com/basenana/friday/core/providers"
	openaisdk "github.com/openai/openai-go"

	"github.com/basenana/friday/core/types"
)

func TestNewClientInsecureSkipVerify(t *testing.T) {
	secure := newClient("https://api.openai.com", "key", Model{Name: "gpt-test", InsecureSkipVerify: false})
	if secure == nil {
		t.Fatal("expected client to be initialized")
	}

	insecure := newClient("https://api.openai.com", "key", Model{Name: "gpt-test", InsecureSkipVerify: true})
	if insecure == nil {
		t.Fatal("expected insecure client to be initialized")
	}
}

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
	resp := newResponse(nil)
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

func TestResponseUpdateUsageTracksCachedPromptTokens(t *testing.T) {
	resp := newResponse(nil)
	resp.updateUsage(openaisdk.CompletionUsage{
		PromptTokens:     120,
		CompletionTokens: 30,
		TotalTokens:      150,
		PromptTokensDetails: openaisdk.CompletionUsagePromptTokensDetails{
			CachedTokens: 80,
		},
	})

	tokens := resp.Tokens()
	if tokens.PromptTokens != 120 {
		t.Fatalf("expected prompt tokens to be tracked, got %d", tokens.PromptTokens)
	}
	if tokens.CachedPromptTokens != 80 {
		t.Fatalf("expected cached prompt tokens to be tracked, got %d", tokens.CachedPromptTokens)
	}
}

func TestChatCompletionNewParamsSetsPromptCacheKeyAndSortsTools(t *testing.T) {
	cli := &client{model: Model{Name: "gpt-test"}}
	req := providers.NewRequest("system prompt", types.Message{Role: types.RoleUser, Content: "hello"})
	req.SetPromptCacheKey("session:root-123")
	req.SetToolDefines([]providers.ToolDefine{
		providers.NewToolDefine("zeta_tool", "zeta", map[string]any{"type": "object"}),
		providers.NewToolDefine("alpha_tool", "alpha", map[string]any{"type": "object"}),
	})

	raw, err := json.Marshal(cli.chatCompletionNewParams(req))
	if err != nil {
		t.Fatalf("failed to marshal chat params: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal chat params: %v", err)
	}

	if decoded["prompt_cache_key"] != "session:root-123" {
		t.Fatalf("expected prompt_cache_key to be propagated, got %#v", decoded["prompt_cache_key"])
	}

	tools, ok := decoded["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected two tools in params, got %#v", decoded["tools"])
	}
	firstTool, _ := tools[0].(map[string]any)
	firstFn, _ := firstTool["function"].(map[string]any)
	if firstFn["name"] != "alpha_tool" {
		t.Fatalf("expected tools to be sorted by name, got %#v", decoded["tools"])
	}
}
