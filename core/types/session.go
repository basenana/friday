package types

import (
	"time"

	"github.com/google/uuid"
)

type SessionType string

const (
	SessionHookBeforeAgent = "before_agent"
	SessionHookBeforeModel = "before_model"
	SessionHookAfterModel  = "after_model"
	SessionHookAfterTool   = "after_tool"
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
	Success bool   `json:"success,omitempty"`
}

// ImageType represents the type of image content
type ImageType string

const (
	ImageTypeURL    ImageType = "url"
	ImageTypeBase64 ImageType = "base64"
)

// ImageContent represents image content in a message
type ImageContent struct {
	Type      ImageType `json:"type"`                 // "url" or "base64"
	URL       string    `json:"url,omitempty"`        // URL for ImageTypeURL
	MediaType string    `json:"media_type,omitempty"` // MIME type for ImageTypeBase64
	Data      string    `json:"data,omitempty"`       // Base64 encoded data for ImageTypeBase64
}

// Message represents a single message in the conversation
// Role determines the type of message, Content is the main text.
// For assistant messages with reasoning, Reasoning contains the thought process.
// ToolCalls contains tool call requests (assistant role).
// ToolResult contains tool execution results (tool role).
// Tokens is the token count for this message:
//   - For assistant messages: output_tokens from API response (or estimated via FuzzyTokens)
//   - For other messages: estimated using FuzzyTokens()
type Message struct {
	Role      MessageRole `json:"role"`
	Content   string      `json:"content,omitempty"`
	Reasoning string      `json:"reasoning,omitempty"`

	// Multimedia content
	Image *ImageContent `json:"image,omitempty"`

	// Tool interaction
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`

	// Tokens is the token count for this message
	Tokens int64 `json:"tokens,omitempty"`

	Metadata map[string]string `json:"-"`
	Time     time.Time         `json:"time,omitempty"`
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
	if m.Tokens != 0 {
		return m.Tokens
	}

	total := len([]rune(m.Content)) + len([]rune(m.Reasoning))

	// Count image tokens (rough estimate)
	if m.Image != nil {
		if m.Image.Type == ImageTypeURL {
			total += len([]rune(m.Image.URL))
		} else if m.Image.Type == ImageTypeBase64 {
			// Base64 images are expensive, estimate ~1000 tokens per image
			total += 1000
		}
	}

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
