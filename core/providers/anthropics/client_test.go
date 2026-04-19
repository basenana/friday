package anthropics

import (
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

func TestMessageCreateParamsCacheControl(t *testing.T) {
	tests := []struct {
		name                  string
		promptCacheKey        string
		messages              []types.Message
		expectSystemCache     bool
		expectMessageCache    bool
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
