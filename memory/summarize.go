package memory

import (
	"context"
	"sync/atomic"

	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/types"
)

type summarizer struct {
	llm        openai.Client
	working    int32
	updateHook func(history []types.Message, abstract string, err error)
}

func (s *summarizer) doSummarize(history []types.Message) {
	if !atomic.CompareAndSwapInt32(&s.working, 0, 1) {
		return
	}

	originHistory := make([]types.Message, len(history))
	for i, msg := range history {
		if msg.OriginToolContent != "" {
			msg.ToolContent = msg.OriginToolContent
		}
		originHistory[i] = msg
	}

	content, err := s.llm.CompletionNonStreaming(context.Background(), openai.NewSimpleRequest(summarizePrompt, originHistory...))
	s.updateHook(originHistory, summaryPrefix+content, err)
	atomic.StoreInt32(&s.working, 0)
}

func newSummarize(llm openai.Client, hook func(history []types.Message, abstract string, err error)) *summarizer {
	return &summarizer{
		llm:        llm,
		working:    0,
		updateHook: hook,
	}
}

const (
	summarizePrompt = `Your job is to summarize a history of previous messages in a conversation between an AI persona and a human.
The conversation you are given is a from a fixed context window and may not be complete.
Messages sent by the AI are marked with the 'assistant' role.
The AI 'assistant' can also make calls to tools, whose outputs can be seen in messages with the 'tool' role.
Things the AI says in the message content are considered inner monologue and are not seen by the user.
The only AI messages seen by the user are from when the AI uses 'send_message'.
Messages the user sends are in the 'user' role.
The 'user' role is also used for important system events, such as login events and heartbeat events (heartbeats run the AI's program without user action, allowing the AI to act without prompting from the user sending them a message).
Summarize what happened in the conversation from the perspective of the AI (use the first person from the perspective of the AI).
Keep your summary less than 1000 words, do NOT exceed this word limit.
Only output the summary, do NOT include anything else in your output, and use the same language as the user input content.

The summary should refer to the template below:

## Basic Information

Participants: Individuals/roles involved in the dialogue
Topic/Purpose: The core issue or goal of the dialogue

## Key Content Extraction

Main Points: Core opinions expressed by each party, preliminary conclusions, and current progress
Points of contention: Existing disagreements or issues for which consensus has not been reached
Action Items: The next steps that have been confirmed but have not yet been implemented
Important Data: Key figures, dates, names, and other hard information involved

## Additional Information

Special Context: Context that may affect understanding (e.g., preconditions, urgency)
Emotional Labeling: Label the emotional state of participants if necessary (e.g., "User expresses dissatisfaction")
`

	summaryPrefix = `Several lengthy dialogues have already taken place. The following is a condensed summary of the progress of these historical dialogues:
`
)
