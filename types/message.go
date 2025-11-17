package types

type Message struct {
	SystemMessage      string `json:"systemMessage,omitempty" xml:"system_message,omitempty"`
	UserMessage        string `json:"userMessage,omitempty" xml:"user_message,omitempty"`
	AssistantMessage   string `json:"assistantMessage,omitempty" xml:"assistant_message,omitempty"`
	AssistantReasoning string `json:"assistantReasoning,omitempty" xml:"assistant_reasoning,omitempty"`
	ToolCallID         string `json:"toolCallID,omitempty" xml:"tool_call_id,omitempty"`
	ToolContent        string `json:"toolContent,omitempty" xml:"tool_content,omitempty"`
	OriginToolContent  string `json:"-"` // for compact
	ImageContent       string `json:"imageContent" xml:"image_content,omitempty"`
}

func (m Message) FuzzyTokens() int64 {
	return int64(float64(len(m.SystemMessage)+len(m.UserMessage)+len(m.AssistantMessage)+len(m.ToolCallID)+len(m.ToolContent)) * 0.5)
}
