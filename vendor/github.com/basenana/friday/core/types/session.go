package types

import (
	"github.com/google/uuid"
)

type SessionType string

const (
	SessionHookBeforeAgent = "before_agent"
	SessionHookBeforeModel = "before_model"
	SessionHookAfterModel  = "after_model"
)

type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
	RoleAgent     MessageRole = "agent"
)

// ToolCall represents a tool call request from the assistant
type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	CallID  string `json:"call_id,omitempty"`
	Content string `json:"content,omitempty"`
}

// Message represents a single message in the conversation
// Role determines the type of message, Content is the main text.
// For assistant messages with reasoning, Reasoning contains the thought process.
// ToolCalls contains tool call requests (assistant role).
// ToolResult contains tool execution results (tool role).
type Message struct {
	Role      MessageRole `json:"role"`
	Content   string      `json:"content,omitempty"`
	Reasoning string      `json:"reasoning,omitempty"`

	// Multimedia content
	ImageURL string `json:"image_url,omitempty"`

	// Tool interaction
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`

	Metadata map[string]string `json:"-"`
	Time     string            `json:"time,omitempty"`
}

func (m Message) GetRole() MessageRole {
	return m.Role
}

func (m Message) IsToolCall() bool {
	return len(m.ToolCalls) > 0
}

func (m Message) IsToolResult() bool {
	return m.ToolResult != nil
}

func (m Message) GetContent() string {
	if m.IsToolResult() {
		return m.ToolResult.Content
	}
	return m.Content
}

func (m Message) FuzzyTokens() int64 {
	total := len([]rune(m.Content)) + len([]rune(m.Reasoning)) + len([]rune(m.ImageURL))

	for _, tc := range m.ToolCalls {
		total += len([]rune(tc.ID)) + len([]rune(tc.Name)) + len([]rune(tc.Arguments))
	}
	if m.ToolResult != nil {
		total += len([]rune(m.ToolResult.CallID)) + len([]rune(m.ToolResult.Content))
	}

	return int64(float64(total) * 0.6)
}

func NewID() string {
	return uuid.New().String()
}
