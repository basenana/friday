package actor

import (
	"github.com/hyponet/eventbus"
	"regexp"
	"sync/atomic"
)

type Mailbox struct {
	name    string
	noReply atomic.Int32
	inbox   chan Letter
}

func (b *Mailbox) CheckInbox() (*Letter, bool) {
	select {
	case letter := <-b.inbox:
		return &letter, b.noReply.Load() > 0
	default:
		return nil, b.noReply.Load() > 0
	}
}

func (b *Mailbox) SendLetter(to, content string) error {
	letter := Letter{
		From:    b.name,
		To:      to,
		Content: content,
	}
	eventbus.Publish("postoffice."+to, letter)
	b.noReply.Add(1)
	return nil
}

func (b *Mailbox) delivered(letter Letter) {
	letter.Content = eraseToolUseInfo(letter.Content)
	b.inbox <- letter
	b.noReply.Add(-1)
}

func newMail(name string) *Mailbox {
	b := &Mailbox{
		name:    name,
		noReply: atomic.Int32{},
		inbox:   make(chan Letter, 10),
	}
	eventbus.Subscribe("postoffice."+name, b.delivered)
	return b
}

type Letter struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Content string `json:"content"`
}

var (
	toolUsePattern = regexp.MustCompile(`<tool_use>\s*<name>.*</name>\s*<arguments>.*</arguments>\s*</tool_use>`)
)

func eraseToolUseInfo(msg string) string {
	return toolUsePattern.ReplaceAllString(msg, "")
}
