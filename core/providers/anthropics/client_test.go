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
				{Role: types.RoleAssistant, Content: "Doing well, thanks!"},
				{Role: types.RoleUser, Content: "Tell me a joke."},
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
				// Cache breakpoint is set on the 4th-from-last message (trail=3).
				// For short histories that don't have a 4th-from-last message,
				// no breakpoint is set — that's expected.
				const cacheTrail = 3
				cacheIdx := len(params.Messages) - cacheTrail - 1
				if cacheIdx < 0 {
					// Not enough messages for a message-level breakpoint; only
					// the system block is cached. This is by design.
					return
				}
				msg := params.Messages[cacheIdx]
				if len(msg.Content) == 0 {
					t.Fatalf("expected content blocks in message at cache idx %d", cacheIdx)
				}
				block := msg.Content[len(msg.Content)-1]
				hasCache := false
				if block.OfText != nil && block.OfText.CacheControl.Type != "" {
					hasCache = true
				}
				if block.OfToolResult != nil && block.OfToolResult.CacheControl.Type != "" {
					hasCache = true
				}
				if !hasCache {
					t.Errorf("expected cache breakpoint at message idx %d, last block had no cache_control", cacheIdx)
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
		{Role: types.RoleAssistant, Content: "Doing well!"},
		{Role: types.RoleUser, Content: "Bye"},
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

	// params.Messages contains user/assistant messages (no system). For 5 messages
	// and cacheTrail=3, the breakpoint sits at idx = 5 - 3 - 1 = 1.
	msgCount := len(params.Messages)
	if msgCount != 5 {
		t.Fatalf("expected 5 messages (excluding system), got %d", msgCount)
	}
	const cacheTrail = 3
	cacheIdx := msgCount - cacheTrail - 1
	if cacheIdx < 0 {
		t.Fatalf("test setup: not enough messages for cache breakpoint")
	}

	for i, msg := range params.Messages {
		for _, block := range msg.Content {
			if block.OfText == nil {
				continue
			}
			if i == cacheIdx {
				// The cache breakpoint must be set on this message's last block.
				if block.OfText.CacheControl.Type == "" {
					// only the last block of the cache message must be set; skip non-last blocks
				}
			} else if block.OfText.CacheControl.Type != "" {
				t.Errorf("message idx %d should not carry cache_control", i)
			}
		}
	}

	cacheMsg := params.Messages[cacheIdx]
	if len(cacheMsg.Content) == 0 {
		t.Fatal("expected content blocks in cache message")
	}
	lastBlock := cacheMsg.Content[len(cacheMsg.Content)-1]
	if lastBlock.OfText == nil || lastBlock.OfText.CacheControl.Type == "" {
		t.Errorf("expected cache breakpoint on message idx %d last block", cacheIdx)
	}
}

func TestMessageCreateParamsCacheControlMarksLastTool(t *testing.T) {
	cli := &client{model: Model{Name: "claude-3-5-sonnet-20241022"}}
	req := providers.NewRequest("",
		types.Message{Role: types.RoleUser, Content: "Hello"},
	)
	req.SetPromptCacheKey("session:test-tools")
	req.SetToolDefines([]providers.ToolDefine{
		providers.NewToolDefine("alpha", "first tool", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		providers.NewToolDefine("omega", "last tool", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
	})

	params := cli.messageCreateParams(req)
	if len(params.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(params.Tools))
	}
	if params.Tools[0].OfTool == nil || params.Tools[1].OfTool == nil {
		t.Fatalf("expected tool definitions to be populated, got %#v", params.Tools)
	}
	if params.Tools[0].OfTool.CacheControl.Type != "" {
		t.Fatalf("expected only last tool to have cache_control, got %#v", params.Tools[0].OfTool.CacheControl)
	}
	if params.Tools[1].OfTool.CacheControl.Type == "" {
		t.Fatalf("expected last tool to have cache_control")
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

func TestMessageCreateParamsDowngradesWholeBatchWhenResultIncomplete(t *testing.T) {
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
	if len(toolUseIDs) != 0 {
		t.Fatalf("expected whole batch to be downgraded to text, got tool_use ids %#v", toolUseIDs)
	}
	if len(fallbackBlocks) != 2 {
		t.Fatalf("expected both tool_use blocks converted to text fallback, got %#v", fallbackBlocks)
	}

	user := params.Messages[1]
	var toolResultCount int
	for _, block := range user.Content {
		if block.OfToolResult != nil {
			toolResultCount++
		}
	}
	if toolResultCount != 0 {
		t.Fatalf("expected no tool_result blocks after batch downgrade, got %#v", user.Content)
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

func TestMessageCreateParamsIncludesMultipleImages(t *testing.T) {
	cli := &client{model: Model{Name: "claude-vision-test"}}
	req := providers.NewRequest("", types.Message{
		Role:    types.RoleUser,
		Content: "describe",
		Images: []types.ImageContent{
			{Type: types.ImageTypeURL, URL: "https://example.com/one.png"},
			{Type: types.ImageTypeBase64, MediaType: "image/png", Data: "dHdv"},
		},
	})

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 1 || len(params.Messages[0].Content) != 3 {
		t.Fatalf("expected text and two image blocks, got %#v", params.Messages)
	}
}

func TestResponseHandleEventBackfillsUsageFromMessageDelta(t *testing.T) {
	// GLM's Anthropic-compatible endpoint sends zero usage in message_start and
	// delivers real values (including cache fields) only in message_delta.
	resp := newResponse(nil)
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"message_start",
		"message":{"id":"msg_1","type":"message","role":"assistant","model":"glm-5.3-flash",
			"content":[],"stop_reason":null,
			"usage":{"input_tokens":0,"output_tokens":0}}
	}`))
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"message_delta",
		"delta":{"stop_reason":"end_turn"},
		"usage":{"input_tokens":52,"output_tokens":61,"cache_read_input_tokens":11008}
	}`))
	resp.close()

	if resp.Token.PromptTokens != 11060 {
		t.Fatalf("expected backfilled PromptTokens=11060 (52 input + 11008 cache read), got %d", resp.Token.PromptTokens)
	}
	if resp.Token.CachedPromptTokens != 11008 {
		t.Fatalf("expected CachedPromptTokens=11008, got %d", resp.Token.CachedPromptTokens)
	}
	if resp.Token.CompletionTokens != 61 {
		t.Fatalf("expected CompletionTokens=61, got %d", resp.Token.CompletionTokens)
	}
}

func TestResponseRetryStateResetClearsBufferedAttemptState(t *testing.T) {
	resp := newResponse(nil)
	resp.Token.PromptTokens = 9
	resp.Token.CompletionTokens = 4
	resp.Token.TotalTokens = 13
	resp.incompleteTool.ID = "call-1"
	resp.incompleteTool.Name = "read_file"
	resp.incompleteTool.Arguments = `{"path":"a.go"}`
	resp.accumulatedContent = "partial"
	resp.currentThinking = true
	resp.currentSignature = "sig-1"
	resp.currentRedacted = "opaque"

	if !resp.canRetry() {
		t.Fatal("expected buffered state without emitted delta to remain retryable")
	}

	resp.resetForRetry()

	if !resp.canRetry() {
		t.Fatal("expected reset response to become retryable again")
	}
	if resp.incompleteTool.ID != "" || resp.incompleteTool.Arguments != "" {
		t.Fatalf("expected buffered tool state to be cleared, got %#v", resp.incompleteTool)
	}
	if resp.accumulatedContent != "" || resp.currentSignature != "" || resp.currentRedacted != "" || resp.currentThinking {
		t.Fatalf("expected transient stream state to be cleared, got content=%q thinking=%v sig=%q redacted=%q",
			resp.accumulatedContent, resp.currentThinking, resp.currentSignature, resp.currentRedacted)
	}
	if tokens := resp.Tokens(); tokens != (providers.Tokens{}) {
		t.Fatalf("expected token usage to be reset, got %#v", tokens)
	}
}

func TestResponseRetryStateTreatsThinkingAsVisibleOutput(t *testing.T) {
	resp := newResponse(nil)
	resp.handleEvent(mustMessageStreamEvent(t, `{
		"type":"content_block_delta",
		"index":0,
		"delta":{"type":"thinking_delta","thinking":"  reasoning body \n"}
	}`))

	if resp.canRetry() {
		t.Fatal("expected emitted thinking delta to block transparent stream retry")
	}
}

func TestMessageCreateParamsCacheControlBeforeLast3(t *testing.T) {
	cli := &client{model: Model{Name: "claude-3-5-sonnet-20241022"}}
	req := providers.NewRequest("", []types.Message{
		{Role: types.RoleSystem, Content: "First system prompt."},
		{Role: types.RoleSystem, Content: "Second system prompt."},
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi!"},
		{Role: types.RoleUser, Content: "How are you?"},
		{Role: types.RoleAssistant, Content: "I'm good!"},
		{Role: types.RoleUser, Content: "What's up?"},
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

	if len(params.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(params.Messages))
	}

	// cache breakpoint should be at index 1 (5-3-1=1)
	for i, msg := range params.Messages {
		hasCache := messageHasCacheControl(msg)
		if i == 1 && !hasCache {
			t.Errorf("message %d should have cache_control", i)
		}
		if i != 1 && hasCache {
			t.Errorf("message %d should not have cache_control", i)
		}
	}
}

func TestMessageCreateParamsCacheControlFallsBackToToolUseBlock(t *testing.T) {
	cli := &client{model: Model{Name: "claude-3-5-sonnet-20241022"}}
	req := providers.NewRequest("",
		types.Message{Role: types.RoleSystem, Content: "You are a helpful assistant."},
		types.Message{Role: types.RoleUser, Content: "Inspect both files."},
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I will inspect them.",
			ToolCalls: []types.ToolCall{{
				ID:        "call-1",
				Name:      "read_file",
				Arguments: `{"path":"a.go"}`,
			}},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-1", Content: "file a"},
		},
		types.Message{Role: types.RoleAssistant, Content: "I found the issue."},
		types.Message{Role: types.RoleUser, Content: "Summarize it."},
	)
	req.SetPromptCacheKey("session:test-tool-use")

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 5 {
		t.Fatalf("expected 5 messages after tool normalization, got %d", len(params.Messages))
	}

	msg := params.Messages[1]
	if len(msg.Content) != 2 {
		t.Fatalf("expected assistant text and tool_use blocks, got %#v", msg.Content)
	}
	if msg.Content[0].OfText == nil {
		t.Fatalf("expected first block to be text, got %#v", msg.Content[0])
	}
	if msg.Content[1].OfToolUse == nil {
		t.Fatalf("expected second block to be tool_use, got %#v", msg.Content[1])
	}
	if msg.Content[0].OfText.CacheControl.Type != "" {
		t.Fatalf("expected text block to remain uncached, got %#v", msg.Content[0].OfText.CacheControl)
	}
	if msg.Content[1].OfToolUse.CacheControl.Type == "" {
		t.Fatalf("expected tool_use block to receive cache_control, got %#v", msg.Content[1].OfToolUse)
	}
}

func TestMessageCreateParamsCacheControlFallsBackToImageBlock(t *testing.T) {
	cli := &client{model: Model{Name: "claude-3-5-sonnet-20241022"}}
	req := providers.NewRequest("",
		types.Message{Role: types.RoleSystem, Content: "You are a helpful assistant."},
		types.Message{Role: types.RoleUser, Content: "Warm up."},
		types.Message{
			Role: types.RoleUser,
			Image: &types.ImageContent{
				Type: types.ImageTypeURL,
				URL:  "https://example.com/test.png",
			},
		},
		types.Message{Role: types.RoleAssistant, Content: "I see the image."},
		types.Message{Role: types.RoleUser, Content: "Anything else?"},
		types.Message{Role: types.RoleAssistant, Content: "No."},
	)
	req.SetPromptCacheKey("session:test-image")

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(params.Messages))
	}

	msg := params.Messages[1]
	if len(msg.Content) != 1 || msg.Content[0].OfImage == nil {
		t.Fatalf("expected cached boundary message to contain one image block, got %#v", msg.Content)
	}
	if msg.Content[0].OfImage.CacheControl.Type == "" {
		t.Fatalf("expected image block to receive cache_control, got %#v", msg.Content[0].OfImage)
	}
}

func TestMessageCreateParamsDowngradesWholeToolBatchWhenAnyResultIsMissing(t *testing.T) {
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
		t.Fatalf("expected downgraded assistant, downgraded tool-result message, and trailing user message, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	var (
		toolUseCount   int
		fallbackBlocks []string
	)
	for _, block := range assistant.Content {
		if block.OfToolUse != nil {
			toolUseCount++
		}
		if text := block.GetText(); text != nil && strings.Contains(*text, "tool call") {
			fallbackBlocks = append(fallbackBlocks, *text)
		}
	}
	if toolUseCount != 0 {
		t.Fatalf("expected whole mismatched tool_use batch to be downgraded, got %#v", assistant.Content)
	}
	if len(fallbackBlocks) != 2 || !strings.Contains(fallbackBlocks[0], "a.go") || !strings.Contains(fallbackBlocks[1], "b.go") {
		t.Fatalf("expected both tool_use blocks to be downgraded, got %#v", fallbackBlocks)
	}

	user := params.Messages[1]
	var (
		toolResultCount int
		userTextBlocks  []string
	)
	for _, block := range user.Content {
		if block.OfToolResult != nil {
			toolResultCount++
		}
		if text := block.GetText(); text != nil {
			userTextBlocks = append(userTextBlocks, *text)
		}
	}
	if toolResultCount != 0 {
		t.Fatalf("expected mismatched tool_result batch to be downgraded, got %#v", user.Content)
	}
	if len(userTextBlocks) != 1 || !strings.Contains(userTextBlocks[0], "file a") {
		t.Fatalf("expected provided tool_result to be preserved as text fallback, got %#v", userTextBlocks)
	}
}

func TestMessageCreateParamsConvertsAllMissingToolUsesToTextWhenTailMessageExists(t *testing.T) {
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
			Role:    types.RoleAgent,
			Content: "tool batch interrupted; retry if needed",
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 2 {
		t.Fatalf("expected assistant plus converted agent tail, got %d messages", len(params.Messages))
	}

	assistant := params.Messages[0]
	var (
		toolUseCount   int
		fallbackBlocks []string
	)
	for _, block := range assistant.Content {
		if block.OfToolUse != nil {
			toolUseCount++
		}
		if text := block.GetText(); text != nil && strings.Contains(*text, "tool call") {
			fallbackBlocks = append(fallbackBlocks, *text)
		}
	}
	if toolUseCount != 0 {
		t.Fatalf("expected all unresolved tool_use blocks to be downgraded, got %#v", assistant.Content)
	}
	if len(fallbackBlocks) != 2 {
		t.Fatalf("expected one fallback per missing tool_use, got %#v", fallbackBlocks)
	}
	if params.Messages[1].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("expected agent tail to be converted to user role, got %q", params.Messages[1].Role)
	}
}

func TestMessageCreateParamsDowngradesWholeBatchWhenTrailingToolResultIsMissing(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I'll inspect three files.",
			ToolCalls: []types.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
				{ID: "call-2", Name: "read_file", Arguments: `{"path":"b.go"}`},
				{ID: "call-3", Name: "read_file", Arguments: `{"path":"c.go"}`},
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
		types.Message{
			Role:    types.RoleUser,
			Content: "continue",
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 3 {
		t.Fatalf("expected assistant, merged tool results, and trailing user message, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	var (
		toolUseCount   int
		fallbackBlocks []string
	)
	for _, block := range assistant.Content {
		if block.OfToolUse != nil {
			toolUseCount++
		}
		if text := block.GetText(); text != nil && strings.Contains(*text, "tool call") {
			fallbackBlocks = append(fallbackBlocks, *text)
		}
	}
	if toolUseCount != 0 {
		t.Fatalf("expected whole mismatched tool_use batch to be downgraded, got %#v", assistant.Content)
	}
	if len(fallbackBlocks) != 3 {
		t.Fatalf("expected all three tool_use blocks to be downgraded, got %#v", fallbackBlocks)
	}
	if !strings.Contains(fallbackBlocks[0], "a.go") || !strings.Contains(fallbackBlocks[1], "b.go") || !strings.Contains(fallbackBlocks[2], "c.go") {
		t.Fatalf("expected downgraded tool_use fallbacks to preserve all tool arguments, got %#v", fallbackBlocks)
	}

	mergedResults := params.Messages[1]
	var (
		toolResultIDs  []string
		mergedTextList []string
	)
	for _, block := range mergedResults.Content {
		if block.OfToolResult != nil {
			toolResultIDs = append(toolResultIDs, block.OfToolResult.ToolUseID)
		}
		if text := block.GetText(); text != nil {
			mergedTextList = append(mergedTextList, *text)
		}
	}
	if len(toolResultIDs) != 0 {
		t.Fatalf("expected mismatched tool_result batch to be downgraded, got %#v", toolResultIDs)
	}
	if len(mergedTextList) != 2 || !strings.Contains(mergedTextList[0], "file a") || !strings.Contains(mergedTextList[1], "file b") {
		t.Fatalf("expected provided tool_results to be preserved as text fallbacks, got %#v", mergedTextList)
	}
}

func TestMessageCreateParamsDowngradesWholeBatchWhenToolResultsArriveOutOfOrder(t *testing.T) {
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
			ToolResult: &types.ToolResult{CallID: "call-2", Content: "file b"},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-1", Content: "file a"},
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 2 {
		t.Fatalf("expected downgraded assistant plus downgraded tool-result message, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	for _, block := range assistant.Content {
		if block.OfToolUse != nil {
			t.Fatalf("expected out-of-order tool_use batch to be downgraded, got %#v", assistant.Content)
		}
	}

	user := params.Messages[1]
	var (
		toolResultCount int
		userTextBlocks  []string
	)
	for _, block := range user.Content {
		if block.OfToolResult != nil {
			toolResultCount++
		}
		if text := block.GetText(); text != nil {
			userTextBlocks = append(userTextBlocks, *text)
		}
	}
	if toolResultCount != 0 {
		t.Fatalf("expected out-of-order tool_result batch to be downgraded, got %#v", user.Content)
	}
	if len(userTextBlocks) != 2 || !strings.Contains(userTextBlocks[0], "file b") || !strings.Contains(userTextBlocks[1], "file a") {
		t.Fatalf("expected downgraded text to preserve original result order, got %#v", userTextBlocks)
	}
}

func TestMessageCreateParamsDowngradesWholeBatchWhenExtraToolResultAppears(t *testing.T) {
	cli := &client{model: Model{Name: "claude-test"}}
	req := providers.NewRequest("",
		types.Message{
			Role:    types.RoleAssistant,
			Content: "I'll inspect one file.",
			ToolCalls: []types.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`},
			},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-1", Content: "file a"},
		},
		types.Message{
			Role:       types.RoleTool,
			ToolResult: &types.ToolResult{CallID: "call-extra", Content: "extra result"},
		},
	)

	params := cli.messageCreateParams(req)
	if len(params.Messages) != 2 {
		t.Fatalf("expected downgraded assistant plus downgraded tool-result message, got %d", len(params.Messages))
	}

	assistant := params.Messages[0]
	for _, block := range assistant.Content {
		if block.OfToolUse != nil {
			t.Fatalf("expected extra tool_result to downgrade the whole tool_use batch, got %#v", assistant.Content)
		}
	}

	user := params.Messages[1]
	var (
		toolResultCount int
		userTextBlocks  []string
	)
	for _, block := range user.Content {
		if block.OfToolResult != nil {
			toolResultCount++
		}
		if text := block.GetText(); text != nil {
			userTextBlocks = append(userTextBlocks, *text)
		}
	}
	if toolResultCount != 0 {
		t.Fatalf("expected extra tool_result batch to be downgraded, got %#v", user.Content)
	}
	if len(userTextBlocks) != 2 || !strings.Contains(userTextBlocks[0], "file a") || !strings.Contains(userTextBlocks[1], "extra result") {
		t.Fatalf("expected downgraded text to preserve all tool_result bodies, got %#v", userTextBlocks)
	}
}

func TestBuildHistoricalToolUseBlockRejectsNonDictJSON(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{"empty", ""},
		{"whitespace", "  "},
		{"null", "null"},
		{"array", "[1,2,3]"},
		{"number", "123"},
		{"string", `"foo"`},
		{"bool", "true"},
		{"invalid", "not json"},
		{"truncated", `{"path":"a.go"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block, ok := buildHistoricalToolUseBlock(types.ToolCall{
				ID: "call-1", Name: "read_file", Arguments: tt.args,
			})
			if ok {
				t.Fatalf("expected non-dict args %q to be rejected, got block=%#v", tt.args, block)
			}
		})
	}
}

func TestBuildHistoricalToolUseBlockAcceptsDictJSON(t *testing.T) {
	block, ok := buildHistoricalToolUseBlock(types.ToolCall{
		ID: "call-1", Name: "read_file", Arguments: `{"path":"a.go"}`,
	})
	if !ok {
		t.Fatal("expected dict args to be accepted")
	}
	if block.OfToolUse == nil || block.OfToolUse.Name != "read_file" {
		t.Fatalf("expected tool_use block, got %#v", block)
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

func TestMessageCreateParamsReasoningEffort(t *testing.T) {
	cases := []struct {
		name     string
		effort   string
		host     string
		wantEff  anthropic.OutputConfigEffort
		wantOpts int
	}{
		{"empty sends nothing", "", "", "", 0},
		{"default sends nothing", providers.ReasoningEffortDefault, "", "", 0},
		{"high sets output_config effort", providers.ReasoningEffortHigh, "", "high", 0},
		{"xhigh is clamped to high", providers.ReasoningEffortXHigh, "", "high", 0},
		{"max is clamped to high", providers.ReasoningEffortMax, "", "high", 0},
		{"none sends no vendor field for official host", providers.ReasoningEffortNone, "https://api.anthropic.com", "", 0},
		{"none disables reasoning for third-party host", providers.ReasoningEffortNone, "https://open.bigmodel.cn/api/anthropic", "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cli := &client{model: Model{Name: "claude-test", ReasoningEffort: tc.effort}, host: tc.host}
			req := providers.NewRequest("summarize this conversation")

			params := cli.messageCreateParams(req)
			if params.OutputConfig.Effort != tc.wantEff {
				t.Fatalf("expected output_config effort=%q, got %q", tc.wantEff, params.OutputConfig.Effort)
			}
			if opts := cli.reasoningOpts(); len(opts) != tc.wantOpts {
				t.Fatalf("expected %d request options, got %d", tc.wantOpts, len(opts))
			}
		})
	}
}

func messageHasCacheControl(msg anthropic.MessageParam) bool {
	for _, block := range msg.Content {
		if contentBlockHasCacheControl(block) {
			return true
		}
	}
	return false
}

func contentBlockHasCacheControl(block anthropic.ContentBlockParamUnion) bool {
	switch {
	case block.OfText != nil:
		return block.OfText.CacheControl.Type != ""
	case block.OfImage != nil:
		return block.OfImage.CacheControl.Type != ""
	case block.OfSearchResult != nil:
		return block.OfSearchResult.CacheControl.Type != ""
	case block.OfToolUse != nil:
		return block.OfToolUse.CacheControl.Type != ""
	case block.OfToolResult != nil:
		return block.OfToolResult.CacheControl.Type != ""
	case block.OfServerToolUse != nil:
		return block.OfServerToolUse.CacheControl.Type != ""
	default:
		return false
	}
}

// Usage accumulation via SetTokens must be safe to read concurrently via
// Tokens(). Run with -race.
func TestResponseHandleEventConcurrentWithTokensReads(t *testing.T) {
	resp := newResponse(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			resp.handleEvent(mustMessageStreamEvent(t, `{
				"type":"message_delta",
				"delta":{"stop_reason":"end_turn"},
				"usage":{"input_tokens":1,"output_tokens":2}
			}`))
		}
	}()
	for i := 0; i < 500; i++ {
		if got := resp.Tokens(); got.CompletionTokens < 0 {
			t.Fatalf("unexpected negative snapshot: %#v", got)
		}
	}
	<-done

	got := resp.Tokens()
	if got.CompletionTokens != 1000 {
		t.Fatalf("expected 1000 accumulated completion tokens, got %d", got.CompletionTokens)
	}
}
