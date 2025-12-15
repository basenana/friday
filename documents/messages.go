package documents

import (
	"bytes"
	"fmt"

	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
)

func SplitMessages(chunkType string, metadata map[string]string, messages []types.Message, config SplitConfig) []*storehouse.Chunk {
	history := covertHistoryMessage2Text(messages)
	return SplitTextContent(chunkType, metadata, history, config)
}

func covertHistoryMessage2Text(messages []types.Message) string {
	buf := &bytes.Buffer{}
	for _, message := range messages {
		card := messageCard(message)
		if card == "" {
			continue
		}
		buf.WriteString(card)
		buf.WriteString("\n")
	}
	return buf.String()
}

func messageCard(message types.Message) string {
	var (
		sender  = "user"
		content string
	)

	switch {
	case message.AssistantMessage != "":
		sender = "assistant"
		content = message.AssistantMessage
	case message.AssistantReasoning != "":
		sender = "assistant"
		content = message.AssistantReasoning
	case message.UserMessage != "":
		sender = "user"
		content = message.UserMessage
	}

	if content == "" {
		return ""
	}

	return fmt.Sprintf("%s [%s]:\n%s", message.Time, sender, content)
}
