package openai

import (
	"github.com/basenana/friday/pkg/tools"
)

type Session struct {
	Prompt  string
	History []Message
	Tools   []tools.Tool
}

type Message struct {
	SystemMessage    string
	UserMessage      string
	AssistantMessage string
	ToolCallID       string
	ToolContent      string
}
