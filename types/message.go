package types

import "encoding/json"

type Message struct {
	SystemMessage      string `json:"systemMessage,omitempty" xml:"system_message,omitempty"`
	UserMessage        string `json:"userMessage,omitempty" xml:"user_message,omitempty"`
	AssistantMessage   string `json:"assistantMessage,omitempty" xml:"assistant_message,omitempty"`
	AssistantReasoning string `json:"assistantReasoning,omitempty" xml:"assistant_reasoning,omitempty"`

	ImageURL string `json:"imageUrl,omitempty" xml:"image_url,omitempty"`

	ToolCallID        string `json:"toolCallID,omitempty" xml:"tool_call_id,omitempty"`
	ToolName          string `json:"toolName,omitempty"`
	ToolArguments     string `json:"toolArguments,omitempty"`
	ToolContent       string `json:"toolContent,omitempty" xml:"tool_content,omitempty"`
	OriginToolContent string `json:"-" xml:"-"` // for compact
}

func (m Message) FuzzyTokens() int64 {
	raw, _ := json.Marshal(m)
	return int64(float64(len([]rune(string(raw)))) * 0.5)
}
