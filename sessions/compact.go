package sessions

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/agents/simple"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

var (
	compactThreshold int64 = 100 * 1000
)

func init() {
	if ctStr := os.Getenv("FRIDAY_MEMORY_COMPACT_THRESHOLD"); ctStr != "" {
		ct, _ := strconv.ParseInt(ctStr, 10, 64)
		if ct > 1000 {
			compactThreshold = ct
		}
	}
}

type MemoryCompact struct {
	simple   *simple.Agent
	session  *Descriptor
	notebook Notebook
	logger   *zap.SugaredLogger
}

func RegisterMemoryCompactHook(llm openai.Client, session *Descriptor) {
	mc := &MemoryCompact{
		simple:   simple.New("compact", "", llm, simple.Option{SystemPrompt: summarizePrompt}),
		session:  session,
		notebook: session.Notebook(),
		logger:   logger.New("compact").With(zap.String("session", session.ID())),
	}
	session.RegisterHooks(mc)
}

func (m *MemoryCompact) GetHooks() map[string][]HookHandler {
	return map[string][]HookHandler{types.SessionHookBeforeModel: {m.compactHistory}}
}

func (m *MemoryCompact) compactHistory(ctx context.Context, payload *types.SessionPayload) error {
	var (
		beforeTokens, afterTokens int64
	)

	for _, msg := range payload.History {
		beforeTokens += msg.FuzzyTokens()
	}

	if beforeTokens < compactThreshold {
		return nil
	}

	m.logger.Infow("historical messages have exceeded the limit and need to be compressed.", "tokens", beforeTokens, "limit", compactThreshold)

	afterTokens = m.compactMessages(ctx, payload)
	compressionRatio := float64(beforeTokens-afterTokens) / float64(beforeTokens)
	m.logger.Infow("compact messages finish",
		"beforeTokens", beforeTokens, "afterTokens", afterTokens, "compressionRatio", compressionRatio)

	if compressionRatio < 0.1 || afterTokens > compactThreshold {
		m.logger.Warn("compact history messages radically")
		return m.summaryMessage(ctx, payload)
	}

	return nil
}

func (m *MemoryCompact) compactMessages(ctx context.Context, payload *types.SessionPayload) int64 {
	var (
		afterTokens int64 = 0
		newHistory  []types.Message
		needKeepIdx = theIndexAfterKeep(len(payload.History))
	)

	for i, msg := range payload.History {
		if msg.AgentMessage != "" {
			continue
		}

		if i < needKeepIdx && msg.ToolCallID != "" && len(msg.ToolContent) > 200 {
			n, err := m.notebook.SaveOrUpdate(ctx, &Note{
				Title:   "Tool Result for " + msg.ToolCallID,
				Content: msg.ToolContent,
			})
			if err != nil {
				m.logger.Errorw("save note for compact error", "err", err.Error())
				afterTokens += msg.FuzzyTokens()
				newHistory = append(newHistory, msg)
				continue
			}

			msg.ToolContent = remindMessage(n.ID)
			afterTokens += msg.FuzzyTokens()
			newHistory = append(newHistory, msg)
			continue
		}

		afterTokens += msg.FuzzyTokens()
		newHistory = append(newHistory, msg)
	}

	payload.History = newHistory

	return afterTokens

}

func (m *MemoryCompact) summaryMessage(ctx context.Context, payload *types.SessionPayload) error {
	history := m.session.contextHistory(ctx, payload.ContextID)
	if len(history) == 0 {
		m.logger.Warnw("origin history is empty")
		return nil
	}

	stream := m.simple.Chat(ctx, &agtapi.Request{
		UserMessage: "Please summarize the historical messages as required, from now on, every character you output will become part of the abstract",
		Memory:      memory.NewEmpty(m.session.ID(), memory.WithHistory(history...)),
	})
	abstract, err := agtapi.ReadAllContent(ctx, stream)
	if err != nil {
		m.logger.Errorw("failed to read abstract", "err", err)
		return err
	}

	if abstract == "" {
		m.logger.Errorw("abstract is empty")
		return nil
	}

	err = m.session.UpdateSummary(ctx, "", abstract)
	if err != nil {
		m.logger.Errorw("failed to update summary", "err", err)
		// skip
	}

	payload.History = m.updateHistoryWithAbstract(payload.History, abstract)
	return nil
}

func (m *MemoryCompact) updateHistoryWithAbstract(history []types.Message, abstract string) []types.Message {
	if abstract == "" || len(history) == 0 {
	}

	m.logger.Infow("abstract history", "abstract", abstract)

	var (
		cutAt      = len(history)
		crtLen     = len(history)
		newHistory []types.Message
		afterToken int64
	)

	if crtLen-cutAt < 5 { // keep 5 newest message
		cutAt = crtLen - 5
	}
	if cutAt < 0 {
		cutAt = 0
	}
	keep := history[cutAt:]

	newHistory = append(newHistory, history[0]) // keep first

	newHistory = append(newHistory, types.Message{AgentMessage: summaryPrefix + abstract})
	newHistory = append(newHistory, keep...)
	for _, msg := range newHistory {
		afterToken += msg.FuzzyTokens()
	}
	m.logger.Infow("update history with abstract finished",
		"beforeHistory", crtLen, "afterHistory", len(newHistory), "afterToken", afterToken)
	return newHistory
}

func (m *MemoryCompact) compactToolUse(ctx context.Context, payload *types.SessionPayload) error {
	notebookCalls := make(map[string]bool)
	for i, msg := range payload.History {
		if msg.ToolName == "" && msg.ToolContent == "" {
			continue
		}

		if msg.ToolName == "retrieve_from_notebook" && msg.ToolCallID != "" {
			notebookCalls[msg.ToolCallID] = true
			continue
		}

		if msg.ToolContent == "" || notebookCalls[msg.ToolCallID] || msg.FuzzyTokens() < 1000 {
			continue
		}

		n, err := m.notebook.SaveOrUpdate(ctx, &Note{
			Title:   "Tool Result for " + msg.ToolCallID,
			Content: msg.ToolContent,
		})
		if err != nil {
			m.logger.Errorw("save note for compact tool use error", "err", err.Error())
			continue
		}

		msg.ToolContent = remindMessage(n.ID)
		payload.History[i] = msg
	}
	return nil
}

func theIndexAfterKeep(msgLen int) int {
	mid := msgLen / 2
	keep5 := msgLen - 5
	if keep5 > msgLen {
		return keep5
	}
	return mid
}

func remindMessage(nid string) string {
	return fmt.Sprintf("The original content was saved in notebook, note id is %s. Use tools to obtain the original text if needed.", nid)
}

const (
	summarizePrompt = `<background>
Your job is to summarize a history of previous messages in a conversation between an AI persona and a human.
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
</background>

<summary_core_objective>
- Based on historical messages, summarize and generate text that conforms to the definition in summary_formatting.
- Do not output any content other than the summary text.
</summary_core_objective>


<summary_formatting>
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
</summary_formatting>
`

	summaryPrefix = `Several lengthy dialogues have already taken place. The following is a condensed summary of the progress of these historical dialogues:
`
)
