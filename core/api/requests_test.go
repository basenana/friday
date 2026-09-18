package api

import (
	"testing"

	"github.com/basenana/friday/core/types"
)

func TestRequestInputMessage(t *testing.T) {
	tests := []struct {
		name     string
		req      Request
		wantRole types.MessageRole
		wantText string
	}{
		{
			name:     "user input",
			req:      Request{UserMessage: "from user"},
			wantRole: types.RoleUser,
			wantText: "from user",
		},
		{
			name:     "agent input",
			req:      Request{AgentMessage: "from agent"},
			wantRole: types.RoleAgent,
			wantText: "from agent",
		},
		{
			name: "agent input takes precedence",
			req: Request{
				UserMessage:  "from user",
				AgentMessage: "from agent",
			},
			wantRole: types.RoleAgent,
			wantText: "from agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, text := tt.req.InputMessage()
			if role != tt.wantRole || text != tt.wantText {
				t.Fatalf("InputMessage() = (%q, %q), want (%q, %q)", role, text, tt.wantRole, tt.wantText)
			}
		})
	}
}

func TestRequestAgentMessageAccessors(t *testing.T) {
	req := &Request{}
	req.SetAgentMessage("internal prompt")
	if got := req.GetAgentMessage(); got != "internal prompt" {
		t.Fatalf("GetAgentMessage() = %q, want %q", got, "internal prompt")
	}
}
