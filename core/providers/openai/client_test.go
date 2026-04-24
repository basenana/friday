package openai

import (
	"encoding/json"
	"strings"
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

	raw, err := json.Marshal(assistantMessageParam(msg, false))
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

func TestAssistantMessageParamThinkingModeSendsEmptyReasoning(t *testing.T) {
	// In thinking mode, assistant messages with no reasoning still need
	// reasoning_content field (empty string) for stricter compatible gateways.
	msg := types.Message{
		Role:    types.RoleAssistant,
		Content: "I will inspect the file.",
	}

	raw, err := json.Marshal(assistantMessageParam(msg, true))
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
	if _, ok := decoded["reasoning_content"]; !ok {
		t.Fatal("expected reasoning_content field to be present in thinking mode, even when empty")
	}
}

func TestAssistantMessageParamNoThinkingModeSkipsReasoning(t *testing.T) {
	msg := types.Message{
		Role:    types.RoleAssistant,
		Content: "Simple response.",
	}

	raw, err := json.Marshal(assistantMessageParam(msg, false))
	if err != nil {
		t.Fatalf("failed to marshal assistant message param: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal assistant message param: %v", err)
	}

	if _, ok := decoded["reasoning_content"]; ok {
		t.Fatal("expected no reasoning_content field when not in thinking mode and no reasoning")
	}
}

func TestChatCompletionNewParamsDetectsThinkingMode(t *testing.T) {
	cli := &client{model: Model{Name: "deepseek-test"}}
	req := providers.NewRequest("",
		types.Message{Role: types.RoleUser, Content: "hello"},
		types.Message{Role: types.RoleAssistant, Content: "Let me think.", Reasoning: "hmm"},
		types.Message{Role: types.RoleUser, Content: "and then?"},
		types.Message{Role: types.RoleAssistant, Content: "Here is the answer."},
	)

	raw, err := json.Marshal(cli.chatCompletionNewParams(req))
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	msgs, ok := decoded["messages"].([]any)
	if !ok || len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	// Last message (second assistant) should have reasoning_content even though
	// its Reasoning field is empty, because thinking mode was detected.
	lastMsg, _ := msgs[3].(map[string]any)
	if _, ok := lastMsg["reasoning_content"]; !ok {
		t.Fatal("expected last assistant message to have reasoning_content in thinking mode")
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

func TestResponseNextChoiceExtractsReasoningFromRawJSONFallback(t *testing.T) {
	var chunk openaisdk.ChatCompletionChunk
	raw := `{
		"id":"0faef1c3-1c2f-4450-9a2b-a0571916357f",
		"object":"chat.completion.chunk",
		"created":1777010004,
		"model":"deepseek-v4-flash",
		"system_fingerprint":"fp_058df29938_prod0820_fp8_kvcache_20260402",
		"choices":[
			{
				"index":0,
				"delta":{
					"content":null,
					"reasoning_content":" say",
					"reasoning_content_signature":"sig-123"
				},
				"logprobs":null,
				"finish_reason":null
			}
		],
		"usage":null
	}`
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatalf("failed to unmarshal chunk: %v", err)
	}

	// Simulate compatible gateways where openai-go does not surface unknown delta fields
	// through ExtraFields and we must parse delta.RawJSON() directly.
	chunk.Choices[0].Delta.JSON.ExtraFields = nil

	resp := newResponse(nil)
	resp.nextChoice(chunk.Choices[0])
	resp.close()

	var deltas []providers.Delta
	for delta := range resp.Message() {
		deltas = append(deltas, delta)
	}

	if len(deltas) != 2 {
		t.Fatalf("expected reasoning and signature deltas, got %#v", deltas)
	}
	if deltas[0].Reasoning != " say" {
		t.Fatalf("expected reasoning delta from raw json fallback, got %#v", deltas[0])
	}
	if deltas[1].ReasoningSignature != "sig-123" {
		t.Fatalf("expected reasoning signature delta from raw json fallback, got %#v", deltas[1])
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

func TestNormalizeOpenAIToolMessagesKeepsPairedToolCallsAndDowngradesOrphans(t *testing.T) {
	messages := normalizeOpenAIToolMessages([]types.Message{
		{Role: types.RoleAssistant, Content: "I'll inspect both files.", ToolCalls: []types.ToolCall{
			{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
			{ID: "call-2", Name: "read_file", Arguments: `{"path":"b.go"}`},
		}},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: "file a"}},
		{Role: types.RoleUser, Content: "continue"},
	})

	if len(messages) != 3 {
		t.Fatalf("expected assistant, tool result, and user messages, got %#v", messages)
	}
	assistant := messages[0]
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call-1" {
		t.Fatalf("expected only paired tool call to remain, got %#v", assistant.ToolCalls)
	}
	if !strings.Contains(assistant.Content, "b.go") {
		t.Fatalf("expected omitted tool call to be downgraded into assistant content, got %q", assistant.Content)
	}
	if messages[1].Role != types.RoleTool || messages[1].ToolResult == nil || messages[1].ToolResult.CallID != "call-1" {
		t.Fatalf("expected paired tool result to remain intact, got %#v", messages[1])
	}
}

func TestNormalizeOpenAIToolMessagesConvertsStandaloneToolResultToUserText(t *testing.T) {
	messages := normalizeOpenAIToolMessages([]types.Message{
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-9", Content: "orphan result"}},
	})

	if len(messages) != 1 {
		t.Fatalf("expected one converted message, got %#v", messages)
	}
	if messages[0].Role != types.RoleUser || messages[0].ToolResult != nil {
		t.Fatalf("expected orphaned tool result to be downgraded to user text, got %#v", messages[0])
	}
	if !strings.Contains(messages[0].Content, "orphan result") {
		t.Fatalf("expected converted content to preserve tool result body, got %q", messages[0].Content)
	}
}

func TestCompatibleChatCompletionNewParamsNormalizesToolHistory(t *testing.T) {
	cli := &compatibleClient{client: &client{model: Model{Name: "compatible-test"}}}
	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I'll inspect both files.",
			ToolCalls: []types.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
				{ID: "call-2", Name: "read_file", Arguments: `{"path":"b.go"}`},
			},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-1", Content: "file a"},
		},
	)

	raw, err := json.Marshal(cli.chatCompletionNewParams(req))
	if err != nil {
		t.Fatalf("failed to marshal chat params: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal chat params: %v", err)
	}

	msgs, ok := decoded["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected assistant and tool messages, got %#v", decoded["messages"])
	}

	assistant, _ := msgs[0].(map[string]any)
	toolCalls, ok := assistant["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected only paired tool call to remain, got %#v", assistant["tool_calls"])
	}
	if content, _ := assistant["content"].(string); !strings.Contains(content, "b.go") {
		t.Fatalf("expected downgraded omitted tool call in assistant content, got %#v", assistant["content"])
	}
}
