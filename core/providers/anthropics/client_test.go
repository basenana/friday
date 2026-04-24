package anthropics

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

func TestNewClientInsecureSkipVerify(t *testing.T) {
	secure := newClient("https://api.anthropic.com", "key", Model{Name: "claude-test", InsecureSkipVerify: false})
	if secure == nil {
		t.Fatal("expected client to be initialized")
	}

	insecure := newClient("https://api.anthropic.com", "key", Model{Name: "claude-test", InsecureSkipVerify: true})
	if insecure == nil {
		t.Fatal("expected insecure client to be initialized")
	}
}

func TestMessageCreateParamsKeepsMixedAssistantTextAndToolUseInSingleMessage(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I will inspect the file.",
			ToolCalls: []types.ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: `{"path":"core/session/compact.go"}`,
			}},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-1", Content: "file content"},
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 2 {
		t.Fatalf("expected assistant + tool result messages, got %d", len(params.Messages))
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

func TestMessageCreateParamsBuildsThinkingFromReasoningFields(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("",
		types.Message{
			Role:               types.RoleAssistant,
			Content:            "I will inspect the file.",
			Reasoning:          "  keep leading and trailing whitespace \n",
			ReasoningSignature: "sig-123",
			RedactedThinking:   "opaque-redacted-payload",
			ToolCalls: []types.ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: `{"path":"core/session/compact.go"}`,
			}},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-1", Content: "file content"},
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 2 {
		t.Fatalf("expected assistant + tool result messages, got %d", len(params.Messages))
	}

	msg := params.Messages[0]
	if len(msg.Content) != 4 {
		t.Fatalf("expected thinking, redacted_thinking, text and tool_use blocks, got %#v", msg.Content)
	}
	if msg.Content[0].OfThinking == nil {
		t.Fatalf("expected first block to be thinking, got %#v", msg.Content[0])
	}
	if got := msg.Content[0].OfThinking.Thinking; got != "  keep leading and trailing whitespace \n" {
		t.Fatalf("expected thinking text to be preserved verbatim, got %#v", got)
	}
	if got := msg.Content[0].OfThinking.Signature; got != "sig-123" {
		t.Fatalf("expected thinking signature to be preserved, got %#v", got)
	}
	if msg.Content[1].OfRedactedThinking == nil {
		t.Fatalf("expected second block to be redacted_thinking, got %#v", msg.Content[1])
	}
	if got := msg.Content[1].OfRedactedThinking.Data; got != "opaque-redacted-payload" {
		t.Fatalf("expected redacted thinking payload to be preserved, got %#v", got)
	}
	if got := msg.Content[2].GetText(); got == nil || *got != "I will inspect the file." {
		t.Fatalf("expected text block after thinking block, got %#v", msg.Content[2])
	}
	if msg.Content[3].OfToolUse == nil || msg.Content[3].OfToolUse.Name != "read_file" {
		t.Fatalf("expected tool_use block after text, got %#v", msg.Content[3])
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

func TestMessageCreateParamsCacheControl(t *testing.T) {
	tests := []struct {
		name               string
		promptCacheKey     string
		messages           []types.Message
		expectSystemCache  bool
		expectMessageCache bool
	}{
		{
			name:           "with cache key, system and user messages",
			promptCacheKey: "session:test-123",
			messages: []types.Message{
				{Role: types.RoleSystem, Content: "You are a helpful assistant."},
				{Role: types.RoleSystem, Content: "Additional context."},
				{Role: types.RoleUser, Content: "Hello"},
			},
			expectSystemCache:  true,
			expectMessageCache: true,
		},
		{
			name:           "without cache key",
			promptCacheKey: "",
			messages: []types.Message{
				{Role: types.RoleSystem, Content: "You are a helpful assistant."},
				{Role: types.RoleUser, Content: "Hello"},
			},
			expectSystemCache:  false,
			expectMessageCache: false,
		},
		{
			name:           "no system messages, cache on message only",
			promptCacheKey: "session:test-123",
			messages: []types.Message{
				{Role: types.RoleUser, Content: "Hello"},
			},
			expectSystemCache:  false,
			expectMessageCache: true,
		},
		{
			name:           "multi-turn conversation",
			promptCacheKey: "session:test-123",
			messages: []types.Message{
				{Role: types.RoleSystem, Content: "You are a helpful assistant."},
				{Role: types.RoleUser, Content: "Hello"},
				{Role: types.RoleAssistant, Content: "Hi there!"},
				{Role: types.RoleUser, Content: "How are you?"},
			},
			expectSystemCache:  true,
			expectMessageCache: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cli := &client{model: Model{Name: "claude-3-5-sonnet-20241022"}}
			req := providers.NewRequest("", tt.messages...)
			req.SetPromptCacheKey(tt.promptCacheKey)

			params := cli.messageCreateParams(req)

			if tt.expectSystemCache {
				if len(params.System) == 0 {
					t.Fatal("expected system blocks but got none")
				}
				lastBlock := params.System[len(params.System)-1]
				if lastBlock.CacheControl.Type == "" {
					t.Error("expected last system block to have cache_control")
				}
			} else if len(params.System) > 0 {
				lastBlock := params.System[len(params.System)-1]
				if lastBlock.CacheControl.Type != "" {
					t.Error("did not expect system block cache_control")
				}
			}

			if tt.expectMessageCache {
				if len(params.Messages) == 0 {
					t.Fatal("expected messages but got none")
				}
				lastMsg := params.Messages[len(params.Messages)-1]
				if len(lastMsg.Content) == 0 {
					t.Fatal("expected content blocks in last message")
				}
				lastContentBlock := lastMsg.Content[len(lastMsg.Content)-1]
				hasCache := false
				if lastContentBlock.OfText != nil && lastContentBlock.OfText.CacheControl.Type != "" {
					hasCache = true
				}
				if lastContentBlock.OfToolResult != nil && lastContentBlock.OfToolResult.CacheControl.Type != "" {
					hasCache = true
				}
				if !hasCache {
					t.Error("expected last message content block to have cache_control")
				}
			} else if len(params.Messages) > 0 {
				for _, msg := range params.Messages {
					for _, block := range msg.Content {
						if block.OfText != nil && block.OfText.CacheControl.Type != "" {
							t.Error("did not expect message cache_control")
						}
						if block.OfToolResult != nil && block.OfToolResult.CacheControl.Type != "" {
							t.Error("did not expect message cache_control")
						}
					}
				}
			}
		})
	}
}

func TestResponseHandleEventAggregatesThinkingSignatureAndRedactedThinking(t *testing.T) {
	resp := newResponse(nil)
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_start",
		"index":0,
		"content_block":{"type":"thinking","thinking":"","signature":"sig-"}
	}`))
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_delta",
		"index":0,
		"delta":{"type":"thinking_delta","thinking":"  reasoning body \n"}
	}`))
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_delta",
		"index":0,
		"delta":{"type":"signature_delta","signature":"part-1"}
	}`))
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_delta",
		"index":0,
		"delta":{"type":"signature_delta","signature":"part-2"}
	}`))
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_stop",
		"index":0
	}`))
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_start",
		"index":1,
		"content_block":{"type":"redacted_thinking","data":"opaque-redacted-payload"}
	}`))
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_stop",
		"index":1
	}`))
	resp.close()

	var deltas []providers.Delta
	for delta := range resp.Message() {
		deltas = append(deltas, delta)
	}

	if len(deltas) != 3 {
		t.Fatalf("expected reasoning delta, signature delta, and redacted thinking delta, got %#v", deltas)
	}
	if deltas[0].Reasoning != "  reasoning body \n" {
		t.Fatalf("expected reasoning delta to preserve whitespace, got %#v", deltas[0])
	}
	if deltas[1].ReasoningSignature != "sig-part-1part-2" {
		t.Fatalf("expected signature fragments to be appended, got %#v", deltas[1])
	}
	if deltas[2].RedactedThinking != "opaque-redacted-payload" {
		t.Fatalf("expected redacted thinking payload to be preserved, got %#v", deltas[2])
	}
}

func mustMessageStreamEvent(t *testing.T, body string) anthropic.MessageStreamEventUnion {
	t.Helper()

	var event anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		t.Fatalf("failed to unmarshal message stream event: %v", err)
	}
	return event
}

func TestMessageCreateParamsCacheControlOnlyOnLastBlocks(t *testing.T) {
	cli := &client{model: Model{Name: "claude-3-5-sonnet-20241022"}}
	req := providers.NewRequest("", []types.Message{
		{Role: types.RoleSystem, Content: "First system prompt."},
		{Role: types.RoleSystem, Content: "Second system prompt."},
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi!"},
		{Role: types.RoleUser, Content: "How are you?"},
	}...)
	req.SetPromptCacheKey("session:test-456")

	params := cli.messageCreateParams(req)

	if len(params.System) != 2 {
		t.Fatalf("expected 2 system blocks, got %d", len(params.System))
	}
	if params.System[0].CacheControl.Type != "" {
		t.Error("first system block should not have cache_control")
	}
	if params.System[1].CacheControl.Type == "" {
		t.Error("last system block should have cache_control")
	}

	if len(params.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(params.Messages))
	}

	for i, msg := range params.Messages[:2] {
		for _, block := range msg.Content {
			if block.OfText != nil && block.OfText.CacheControl.Type != "" {
				t.Errorf("message %d should not have cache_control", i)
			}
		}
	}

	lastMsg := params.Messages[2]
	if len(lastMsg.Content) == 0 {
		t.Fatal("expected content in last message")
	}
	lastBlock := lastMsg.Content[len(lastMsg.Content)-1]
	if lastBlock.OfText == nil || lastBlock.OfText.CacheControl.Type == "" {
		t.Error("last message last content block should have cache_control")
	}
}

func TestMessageCreateParamsSanitizesOrphanedToolUse(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	// First assistant has tool_use "tc-orphan" with no corresponding tool_result,
	// and it's NOT the last assistant message — so it's truly orphaned.
	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I called a tool.",
			ToolCalls: []types.ToolCall{
				{ID: "tc-orphan", Name: "read_file", Arguments: `{"path":"a.go"}`},
			},
		},
		types.Message{
			Role:    types.RoleUser,
			Content: "What happened?",
		},
		types.Message{
			Role:    types.RoleAssistant,
			Content: "Here is the answer.",
		},
	)

	params := cli.messageCreateParams(req)
	// Should have 3 messages: first assistant, user, second assistant
	if len(params.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(params.Messages))
	}
	// The first assistant message should have text + converted-to-text orphaned tool_use
	assistant := params.Messages[0]
	if len(assistant.Content) != 2 {
		t.Fatalf("expected 2 content blocks (text + converted tool_use), got %d", len(assistant.Content))
	}
	if assistant.Content[1].OfToolUse != nil {
		t.Fatalf("expected orphaned tool_use to be converted to text, got tool_use block")
	}
	if got := assistant.Content[1].GetText(); got == nil || !strings.Contains(*got, "tool call") {
		t.Fatalf("expected converted text to mention tool call, got %#v", got)
	}
}

func TestMessageCreateParamsMergesImmediateToolResultsIntoSingleUserMessage(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
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
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-2", Content: "file b"},
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 2 {
		t.Fatalf("expected assistant + merged user tool result messages, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	var toolUseCount int
	for _, block := range assistant.Content {
		if block.OfToolUse != nil {
			toolUseCount++
		}
	}
	if toolUseCount != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %#v", assistant.Content)
	}

	user := params.Messages[1]
	if user.Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected merged tool result role=user, got %q", user.Role)
	}

	var toolResultIDs []string
	for _, block := range user.Content {
		if block.OfToolResult != nil {
			toolResultIDs = append(toolResultIDs, block.OfToolResult.ToolUseID)
		}
	}
	if len(toolResultIDs) != 2 {
		t.Fatalf("expected 2 tool_result blocks in one user message, got %#v", user.Content)
	}
	if toolResultIDs[0] != "call-1" || toolResultIDs[1] != "call-2" {
		t.Fatalf("expected merged tool_result IDs to preserve order, got %#v", toolResultIDs)
	}
}

func TestMessageCreateParamsKeepsOnlyToolUsesWithImmediateResults(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
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
		types.Message{
			Role:    types.RoleUser,
			Content: "continue",
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 3 {
		t.Fatalf("expected assistant, merged tool result, and user text messages, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	var (
		toolUseIDs     []string
		fallbackBlocks []string
	)
	for _, block := range assistant.Content {
		if block.OfToolUse != nil {
			toolUseIDs = append(toolUseIDs, block.OfToolUse.ID)
		}
		if text := block.GetText(); text != nil && strings.Contains(*text, "tool call") {
			fallbackBlocks = append(fallbackBlocks, *text)
		}
	}
	if len(toolUseIDs) != 1 || toolUseIDs[0] != "call-1" {
		t.Fatalf("expected only immediately paired tool_use to remain, got %#v", toolUseIDs)
	}
	if len(fallbackBlocks) != 1 || !strings.Contains(fallbackBlocks[0], "b.go") {
		t.Fatalf("expected missing tool_use to be converted to text fallback, got %#v", fallbackBlocks)
	}

	user := params.Messages[1]
	var toolResultCount int
	for _, block := range user.Content {
		if block.OfToolResult != nil {
			toolResultCount++
		}
	}
	if toolResultCount != 1 {
		t.Fatalf("expected only one immediate tool_result block, got %#v", user.Content)
	}
}

func TestMessageCreateParamsConvertsUnresolvedFinalToolUseToText(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I called a tool.",
			ToolCalls: []types.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
			},
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 1 {
		t.Fatalf("expected one assistant message, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	if len(assistant.Content) != 2 {
		t.Fatalf("expected text + fallback block, got %#v", assistant.Content)
	}
	if assistant.Content[1].OfToolUse != nil {
		t.Fatalf("expected unresolved final tool_use to be converted to text, got %#v", assistant.Content[1])
	}
	if got := assistant.Content[1].GetText(); got == nil || !strings.Contains(*got, "tool call") {
		t.Fatalf("expected fallback text for unresolved final tool_use, got %#v", assistant.Content[1])
	}
}
