package memory

import (
	"testing"

	"github.com/basenana/friday/core/types"
)

func TestFormatConversation(t *testing.T) {
	tests := []struct {
		name     string
		messages []types.Message
		want     string
	}{
		{
			name:     "empty messages",
			messages: []types.Message{},
			want:     "",
		},
		{
			name: "user message",
			messages: []types.Message{
				{Role: types.RoleUser, Content: "Hello"},
			},
			want: "USER: Hello\n\n",
		},
		{
			name: "assistant message",
			messages: []types.Message{
				{Role: types.RoleAssistant, Content: "Hi there"},
			},
			want: "ASSISTANT: Hi there\n\n",
		},
		{
			name: "assistant with reasoning",
			messages: []types.Message{
				{Role: types.RoleAssistant, Content: "Response", Reasoning: "Let me think..."},
			},
			want: "ASSISTANT [thinking]: Let me think...\nASSISTANT: Response\n\n",
		},
		{
			name: "assistant with content and tool call",
			messages: []types.Message{
				{
					Role:    types.RoleAssistant,
					Content: "I will inspect the file.",
					ToolCalls: []types.ToolCall{{
						Name:      "read_file",
						Arguments: `{"path":"core/session/compact.go"}`,
					}},
				},
			},
			want: "ASSISTANT: I will inspect the file.\nASSISTANT TOOL CALL: read_file({\"path\":\"core/session/compact.go\"})\n\n",
		},
		{
			name: "assistant with tool call only",
			messages: []types.Message{
				{
					Role: types.RoleAssistant,
					ToolCalls: []types.ToolCall{{
						Name:      "list_dir",
						Arguments: `{"path":"core/session"}`,
					}},
				},
			},
			want: "ASSISTANT TOOL CALL: list_dir({\"path\":\"core/session\"})\n\n",
		},
		{
			name: "tool result",
			messages: []types.Message{
				{Role: types.RoleTool, ToolResult: &types.ToolResult{Content: "tool output"}},
			},
			want: "TOOL RESULT: tool output\n\n",
		},
		{
			name: "multiple messages",
			messages: []types.Message{
				{Role: types.RoleUser, Content: "Question"},
				{Role: types.RoleAssistant, Content: "Answer"},
			},
			want: "USER: Question\n\nASSISTANT: Answer\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatConversation(tt.messages)
			if got != tt.want {
				t.Errorf("FormatConversation() = %q, want %q", got, tt.want)
			}
		})
	}
}
