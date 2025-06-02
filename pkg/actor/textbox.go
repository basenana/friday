package actor

import (
	"github.com/basenana/friday/pkg/agent"
	"regexp"
)

type Textbox struct {
}

func (b *Textbox) CheckInbox() (*Text, bool) {
	return nil, false
}

func (b *Textbox) WaitReply(text string, reply *agent.Reply) error {
	return nil
}

type Text struct {
	Text  string `json:"text"`
	Reply string `json:"reply"`
}

var (
	toolUsePattern = regexp.MustCompile(`<tool_use>\s*<name>.*</name>\s*<arguments>.*</arguments>\s*</tool_use>`)
)

func eraseToolUseInfo(msg string) string {
	return toolUsePattern.ReplaceAllString(msg, "")
}
