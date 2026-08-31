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

func TestNormalizeOpenAIToolMessagesDowngradesWholeBatchWhenAnyResultIsMissing(t *testing.T) {
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
	if len(assistant.ToolCalls) != 0 {
		t.Fatalf("expected whole tool_calls batch to be downgraded, got %#v", assistant.ToolCalls)
	}
	if !strings.Contains(assistant.Content, "a.go") || !strings.Contains(assistant.Content, "b.go") {
		t.Fatalf("expected both tool calls to be downgraded into assistant content, got %q", assistant.Content)
	}
	if messages[1].Role != types.RoleUser || messages[1].ToolResult != nil {
		t.Fatalf("expected unpaired tool result to be converted to user text, got %#v", messages[1])
	}
	if !strings.Contains(messages[1].Content, "file a") {
		t.Fatalf("expected converted tool result to preserve body, got %q", messages[1].Content)
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
	if toolCalls, ok := assistant["tool_calls"].([]any); ok && len(toolCalls) > 0 {
		t.Fatalf("expected whole tool_calls batch to be downgraded when any result is missing, got %#v", toolCalls)
	}
	content, _ := assistant["content"].(string)
	if !strings.Contains(content, "a.go") || !strings.Contains(content, "b.go") {
		t.Fatalf("expected both omitted tool calls in assistant content, got %#v", content)
	}
}

func TestResponseNextChoiceExtractsReasoningDetails(t *testing.T) {
	var chunk openaisdk.ChatCompletionChunk
	raw := `{
		"id":"minimax-stream-1",
		"object":"chat.completion.chunk",
		"created":1777010004,
		"model":"MiniMax-M2.5",
		"choices":[
			{
				"index":0,
				"delta":{
					"reasoning_details":[
						{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","index":1,"text":"second"},
						{"type":"reasoning.text","id":"reasoning-text-0","format":"MiniMax-response-v1","index":0,"text":"first"}
					]
				},
				"finish_reason":null
			}
		],
		"usage":null
	}`
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatalf("failed to unmarshal chunk: %v", err)
	}

	resp := newResponse(nil)
	resp.nextChoice(chunk.Choices[0])
	resp.close()

	var deltas []providers.Delta
	for delta := range resp.Message() {
		deltas = append(deltas, delta)
	}

	if len(deltas) != 2 || deltas[0].Reasoning != "first" || deltas[1].Reasoning != "second" {
		t.Fatalf("expected indexed reasoning_details deltas in order, got %#v", deltas)
	}
}

func TestResponseRetryStateResetClearsBufferedAttemptState(t *testing.T) {
	resp := newResponse(nil)
	resp.updateUsage(openaisdk.CompletionUsage{
		PromptTokens:     12,
		CompletionTokens: 7,
		TotalTokens:      19,
	})
	resp.nextChoice(openaisdk.ChatCompletionChunkChoice{
		Delta: openaisdk.ChatCompletionChunkChoiceDelta{
			ToolCalls: []openaisdk.ChatCompletionChunkChoiceDeltaToolCall{{
				ID: "call-1",
				Function: openaisdk.ChatCompletionChunkChoiceDeltaToolCallFunction{
					Name:      "read_file",
					Arguments: `{"path":"a.go"}`,
				},
			}},
		},
	})

	if !resp.canRetry() {
		t.Fatal("expected buffered tool call without emitted delta to remain retryable")
	}
	if resp.incompleteTool.ID == "" {
		t.Fatal("expected incomplete tool call to be buffered")
	}

	resp.resetForRetry()

	if !resp.canRetry() {
		t.Fatal("expected reset response to become retryable again")
	}
	if resp.incompleteTool.ID != "" || resp.incompleteTool.Arguments != "" {
		t.Fatalf("expected buffered tool call to be cleared, got %#v", resp.incompleteTool)
	}
	if resp.accumulatedContent != "" {
		t.Fatalf("expected accumulated content to be cleared, got %q", resp.accumulatedContent)
	}
	if tokens := resp.Tokens(); tokens != (providers.Tokens{}) {
		t.Fatalf("expected token usage to be reset, got %#v", tokens)
	}
}

func TestResponseRetryStateTreatsReasoningAsVisibleOutput(t *testing.T) {
	var chunk openaisdk.ChatCompletionChunk
	raw := `{
		"id":"stream-1",
		"object":"chat.completion.chunk",
		"created":1777010004,
		"model":"deepseek-v4-flash",
		"choices":[
			{
				"index":0,
				"delta":{
					"content":null,
					"reasoning_content":"thinking",
					"reasoning_content_signature":"sig-123"
				},
				"finish_reason":null
			}
		]
	}`
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatalf("failed to unmarshal chunk: %v", err)
	}
	chunk.Choices[0].Delta.JSON.ExtraFields = nil

	resp := newResponse(nil)
	resp.nextChoice(chunk.Choices[0])

	if resp.canRetry() {
		t.Fatal("expected reasoning/signature deltas to block transparent stream retry")
	}
}

func TestChatCompletionNewParamsIncludesMultipleImages(t *testing.T) {
	cli := &client{model: Model{Name: "vision-test"}}
	req := providers.NewRequest("", types.Message{
		Role:    types.RoleUser,
		Content: "describe",
		Images: []types.ImageContent{
			{Type: types.ImageTypeURL, URL: "https://example.com/one.png"},
			{Type: types.ImageTypeBase64, MediaType: "image/png", Data: "dHdv"},
		},
	})

	raw, err := json.Marshal(cli.chatCompletionNewParams(req))
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var decoded struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if len(decoded.Messages) != 1 || len(decoded.Messages[0].Content) != 3 {
		t.Fatalf("expected text and two image parts, got %s", raw)
	}
}

func TestResponseNormalizesThinkTagsAcrossChunks(t *testing.T) {
	resp := newResponse(nil)
	resp.nextChoice(openaisdk.ChatCompletionChunkChoice{Delta: openaisdk.ChatCompletionChunkChoiceDelta{Content: "<thi"}})
	resp.nextChoice(openaisdk.ChatCompletionChunkChoice{Delta: openaisdk.ChatCompletionChunkChoiceDelta{Content: "nk>analysis</think>answer"}})
	resp.close()

	var deltas []providers.Delta
	for delta := range resp.Message() {
		deltas = append(deltas, delta)
	}
	if len(deltas) != 2 || deltas[0].Reasoning != "analysis" || deltas[1].Content != "answer" {
		t.Fatalf("unexpected normalized deltas: %#v", deltas)
	}
}

func TestNormalizeOpenAIToolMessagesDoesNotCrossPairAcrossUserTurn(t *testing.T) {
	messages := normalizeOpenAIToolMessages([]types.Message{
		{Role: types.RoleAssistant, Content: "I'll inspect it.", ToolCalls: []types.ToolCall{
			{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		}},
		{Role: types.RoleUser, Content: "wait"},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: "late result"}},
	})

	if len(messages) != 3 {
		t.Fatalf("expected assistant, user, and downgraded tool result messages, got %#v", messages)
	}
	if len(messages[0].ToolCalls) != 0 {
		t.Fatalf("expected non-immediate tool call to be downgraded, got %#v", messages[0].ToolCalls)
	}
	if !strings.Contains(messages[0].Content, "tool call read_file") {
		t.Fatalf("expected downgraded tool call in assistant content, got %q", messages[0].Content)
	}
	if messages[2].Role != types.RoleUser || messages[2].ToolResult != nil {
		t.Fatalf("expected late tool result to be converted to user text, got %#v", messages[2])
	}
	if !strings.Contains(messages[2].Content, "late result") {
		t.Fatalf("expected converted tool result to preserve content, got %q", messages[2].Content)
	}
}

func TestNormalizeOpenAIToolMessagesDowngradesWholeBatchWhenResultsArriveOutOfOrder(t *testing.T) {
	messages := normalizeOpenAIToolMessages([]types.Message{
		{Role: types.RoleAssistant, Content: "I'll inspect both files.", ToolCalls: []types.ToolCall{
			{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
			{ID: "call-2", Name: "read_file", Arguments: `{"path":"b.go"}`},
		}},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-2", Content: "file b"}},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: "file a"}},
	})

	if len(messages) != 3 {
		t.Fatalf("expected assistant plus two downgraded tool result messages, got %#v", messages)
	}
	if len(messages[0].ToolCalls) != 0 {
		t.Fatalf("expected out-of-order tool batch to be downgraded, got %#v", messages[0].ToolCalls)
	}
	if !strings.Contains(messages[0].Content, "a.go") || !strings.Contains(messages[0].Content, "b.go") {
		t.Fatalf("expected downgraded assistant content to preserve both tool calls, got %q", messages[0].Content)
	}
	if messages[1].Role != types.RoleUser || messages[1].ToolResult != nil || !strings.Contains(messages[1].Content, "file b") {
		t.Fatalf("expected first out-of-order result to be downgraded, got %#v", messages[1])
	}
	if messages[2].Role != types.RoleUser || messages[2].ToolResult != nil || !strings.Contains(messages[2].Content, "file a") {
		t.Fatalf("expected second out-of-order result to be downgraded, got %#v", messages[2])
	}
}

func TestNormalizeOpenAIToolMessagesDowngradesWholeBatchWhenExtraResultAppears(t *testing.T) {
	messages := normalizeOpenAIToolMessages([]types.Message{
		{Role: types.RoleAssistant, Content: "I'll inspect one file.", ToolCalls: []types.ToolCall{
			{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		}},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: "file a"}},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-extra", Content: "extra result"}},
	})

	if len(messages) != 3 {
		t.Fatalf("expected assistant plus two downgraded tool result messages, got %#v", messages)
	}
	if len(messages[0].ToolCalls) != 0 {
		t.Fatalf("expected extra tool result to downgrade the whole tool batch, got %#v", messages[0].ToolCalls)
	}
	if !strings.Contains(messages[0].Content, "a.go") {
		t.Fatalf("expected downgraded assistant content to preserve original tool call, got %q", messages[0].Content)
	}
	if messages[1].Role != types.RoleUser || messages[1].ToolResult != nil || !strings.Contains(messages[1].Content, "file a") {
		t.Fatalf("expected matched result to be downgraded with the batch, got %#v", messages[1])
	}
	if messages[2].Role != types.RoleUser || messages[2].ToolResult != nil || !strings.Contains(messages[2].Content, "extra result") {
		t.Fatalf("expected extra result to be downgraded, got %#v", messages[2])
	}
}

func TestNormalizeOpenAIToolMessagesDowngradesWholeBatchWhenAnyToolCallArgumentsAreInvalid(t *testing.T) {
	messages := normalizeOpenAIToolMessages([]types.Message{
		{Role: types.RoleAssistant, Content: "I'll inspect one file.", ToolCalls: []types.ToolCall{
			{ID: "call-1", Name: "read_file", Arguments: `null`},
		}},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: "bad args"}},
	})

	if len(messages) != 2 {
		t.Fatalf("expected assistant plus downgraded tool result messages, got %#v", messages)
	}
	if len(messages[0].ToolCalls) != 0 {
		t.Fatalf("expected invalid-args tool batch to be downgraded, got %#v", messages[0].ToolCalls)
	}
	if !strings.Contains(messages[0].Content, "null") {
		t.Fatalf("expected downgraded assistant content to preserve invalid arguments, got %q", messages[0].Content)
	}
	if messages[1].Role != types.RoleUser || messages[1].ToolResult != nil || !strings.Contains(messages[1].Content, "bad args") {
		t.Fatalf("expected tool result to be downgraded with invalid tool batch, got %#v", messages[1])
	}
}

func TestFlushToolUseReplacesNonDictArgumentsWithEmptyObjectAndError(t *testing.T) {
	tests := []struct {
		name      string
		args      string
		wantArgs  string
		wantError string
	}{
		{"dict unchanged", `{"path":"a.go"}`, `{"path":"a.go"}`, ``},
		{"empty object unchanged", `{}`, `{}`, ``},
		{"array replaced", `[1,2,3]`, `{}`, `tool read_file: arguments must be a JSON object, got: [1,2,3]`},
		{"number replaced", `42`, `{}`, `tool read_file: arguments must be a JSON object, got: 42`},
		{"null replaced", `null`, `{}`, `tool read_file: arguments must be a JSON object, got: null`},
		{"string replaced", `"foo"`, `{}`, `tool read_file: arguments must be a JSON object, got: "foo"`},
		{"invalid replaced", `not json`, `{}`, `tool read_file: arguments must be a JSON object, got: not json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := newResponse(nil)
			resp.incompleteTool.ID = "call-1"
			resp.incompleteTool.Name = "read_file"
			resp.incompleteTool.Arguments = tt.args

			resp.flushToolUse()
			resp.close()

			var got *providers.ToolCall
			for delta := range resp.Message() {
				if len(delta.ToolUse) > 0 {
					tc := delta.ToolUse[0]
					got = &tc
				}
			}
			if got == nil {
				t.Fatalf("expected emit with args, got no emit")
			}
			if got.Arguments != tt.wantArgs || got.Error != tt.wantError {
				t.Fatalf("expected args/error (%q, %q), got (%q, %q)", tt.wantArgs, tt.wantError, got.Arguments, got.Error)
			}
		})
	}
}

func TestXMLBodyToMessageNormalizesInvalidToolArguments(t *testing.T) {
	msg := xmlBodyToMessage(`<tool_use><id>call-1</id><name>read_file</name><arguments>[1,2,3]</arguments></tool_use>`)
	if msg == nil || len(msg.ToolUse) != 1 {
		t.Fatalf("expected one tool_use message, got %#v", msg)
	}
	if msg.ToolUse[0].Arguments != "{}" {
		t.Fatalf("expected normalized arguments, got %#v", msg.ToolUse[0])
	}
	if msg.ToolUse[0].Error != `tool read_file: arguments must be a JSON object, got: [1,2,3]` {
		t.Fatalf("expected raw-args error message, got %#v", msg.ToolUse[0])
	}
}

func TestChatCompletionNewParamsReasoningEffort(t *testing.T) {
	cases := []struct {
		name            string
		effort          string
		host            string
		wantEffortField any
		wantOpts        int
	}{
		{"empty sends nothing", "", "", nil, 0},
		{"default sends nothing", providers.ReasoningEffortDefault, "", nil, 0},
		{"high sets reasoning_effort", providers.ReasoningEffortHigh, "", "high", 0},
		{"xhigh is sent as-is", providers.ReasoningEffortXHigh, "", "xhigh", 0},
		{"none sends no vendor fields for official host", providers.ReasoningEffortNone, "https://api.openai.com/v1", nil, 0},
		{"none disables thinking for third-party host", providers.ReasoningEffortNone, "https://api.minimax.chat/v1", nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := &client{model: Model{Name: "gpt-test", ReasoningEffort: tc.effort}, host: tc.host}
			req := providers.NewRequest("system", types.Message{Role: types.RoleUser, Content: "hello"})

			raw, err := json.Marshal(cli.chatCompletionNewParams(req))
			if err != nil {
				t.Fatalf("failed to marshal chat params: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("failed to unmarshal chat params: %v", err)
			}

			if got, ok := decoded["reasoning_effort"]; tc.wantEffortField == nil && ok {
				t.Fatalf("expected no reasoning_effort field, got %#v", got)
			} else if tc.wantEffortField != nil && got != tc.wantEffortField {
				t.Fatalf("expected reasoning_effort=%#v, got %#v", tc.wantEffortField, got)
			}

			if opts := cli.reasoningOpts(); len(opts) != tc.wantOpts {
				t.Fatalf("expected %d request options, got %d", tc.wantOpts, len(opts))
			}
		})
	}
}

// The MiniMax-style reasoning_split / thinking fields must only be attached
// for third-party hosts; api.openai.com rejects unknown top-level fields.
func TestReasoningOptsGatedByHost(t *testing.T) {
	model := Model{Name: "minimax-m2", ReasoningSplit: true, ReasoningEffort: providers.ReasoningEffortNone}

	official := &client{model: model, host: "https://api.openai.com/v1"}
	if opts := official.reasoningOpts(); len(opts) != 0 {
		t.Fatalf("expected no vendor fields for official host, got %d opts", len(opts))
	}

	thirdParty := &client{model: model, host: "https://api.minimax.chat/v1"}
	if opts := thirdParty.reasoningOpts(); len(opts) != 2 {
		t.Fatalf("expected reasoning_split and thinking opts for third-party host, got %d", len(opts))
	}

	unset := &client{model: model}
	if opts := unset.reasoningOpts(); len(opts) != 0 {
		t.Fatalf("expected no vendor fields for default host, got %d opts", len(opts))
	}
}
