package types

type Message struct {
	SystemMessage      string `json:"system_message,omitempty"`
	UserMessage        string `json:"user_message,omitempty"`
	AssistantMessage   string `json:"assistant_message,omitempty"`
	AssistantReasoning string `json:"assistant_reasoning,omitempty"`

	ImageURL string `json:"image_url,omitempty"`

	ToolCallID            string `json:"tool_call_id,omitempty"`
	ToolName              string `json:"tool_name,omitempty"`
	ToolArguments         string `json:"tool_arguments,omitempty"`
	ToolContent           string `json:"tool_content,omitempty"`
	SimplifiedToolContent string `json:"-"`

	Time string `json:"time,omitempty"`
}

func (m Message) FuzzyTokens() int64 {
	counter := []int{
		len([]rune(m.SystemMessage)),
		len([]rune(m.UserMessage)),
		len([]rune(m.AssistantMessage)),
		len([]rune(m.ImageURL)),
		len([]rune(m.ToolCallID)),
		len([]rune(m.ToolName)),
		len([]rune(m.ToolArguments)),
	}

	if m.ToolContent != "" {
		if m.SimplifiedToolContent != "" {
			counter = append(counter, len([]rune(m.SimplifiedToolContent)))
		} else {
			counter = append(counter, len([]rune(m.ToolContent)))
		}
	}

	var total float64
	for _, c := range counter {
		total += float64(c)
	}

	return int64(total * 0.6)
}
