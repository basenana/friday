package sessions

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	agtapi2 "github.com/basenana/friday/core/agents/agtapi"
	"github.com/basenana/friday/core/agents/summarize"
	"github.com/basenana/friday/core/memory"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/storehouse"
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
	summary    *summarize.Agent
	session    *Descriptor
	store      storehouse.Storehouse
	scratchpad tools.Scratchpad
	logger     *zap.SugaredLogger
}

func RegisterMemoryCompactHook(llm openai.Client, session *Descriptor) {
	mc := &MemoryCompact{
		summary:    summarize.New("compacter", "", llm, summarize.Option{}),
		session:    session,
		store:      session.store,
		scratchpad: session.Scratchpad(),
		logger:     logger.New("compact").With(zap.String("session", session.ID())),
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

		if i < needKeepIdx && msg.ToolCallID != "" && len(msg.ToolContent) > 500 {
			n, err := m.scratchpad.WriteNote(ctx, &tools.ScratchpadNote{
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
	history := m.contextHistory(ctx, payload.ContextID)
	if len(history) == 0 {
		m.logger.Warnw("origin history is empty")
		return nil
	}

	stream := m.summary.Chat(ctx, &agtapi2.Request{
		Session:     m.session.Session(),
		UserMessage: "Please summarize the historical messages as required, from now on, every character you output will become part of the abstract",
		Memory:      memory.NewEmpty(m.session.ID(), memory.WithHistory(history...), memory.WithUnlimitedSession()),
	})
	abstract, err := agtapi2.ReadAllContent(ctx, stream)
	if err != nil {
		m.logger.Errorw("failed to read abstract", "err", err)
		return err
	}

	if abstract == "" {
		m.logger.Errorw("abstract is empty")
		return nil
	}

	if payload.ContextID == m.session.ID() {
		err = m.session.UpdateSummary(ctx, "", abstract)
		if err != nil {
			m.logger.Errorw("failed to update summary", "err", err)
			// skip
		}
	}

	payload.History = m.updateHistoryWithAbstract(payload.History, abstract)
	return nil
}

func (m *MemoryCompact) contextHistory(ctx context.Context, contextID string) []types.Message {
	allMessages, err := m.store.ListMessages(ctx, m.session.ID())
	if err != nil {
		return nil
	}

	var result []types.Message
	for _, msg := range allMessages {
		ctxID, ok := msg.Metadata["context_id"]
		if !ok || ctxID == "" {
			continue
		}

		if strings.HasPrefix(contextID, ctxID) {
			result = append(result, *msg)
		}
	}

	return result
}

func (m *MemoryCompact) updateHistoryWithAbstract(history []types.Message, abstract string) []types.Message {
	if abstract == "" || len(history) == 0 {
		return history
	}

	effectiveHistory := make([]types.Message, 0, len(history))
	for _, msg := range history {
		if msg.UserMessage == "" && msg.AssistantMessage == "" {
			continue
		}
		effectiveHistory = append(effectiveHistory, msg)
	}

	if len(effectiveHistory) == 0 {
		return history
	}

	m.logger.Infow("abstract history", "abstract", abstract)

	var (
		cutAt      = len(effectiveHistory) - 5
		newHistory []types.Message
		afterToken int64
	)

	if cutAt < 0 {
		cutAt = 0
	}
	keep := effectiveHistory[cutAt:]

	if cutAt > 0 {
		newHistory = append(newHistory, effectiveHistory[0]) // keep first
	}

	newHistory = append(newHistory, types.Message{AgentMessage: summaryPrefix + abstract})
	newHistory = append(newHistory, keep...)
	for _, msg := range newHistory {
		afterToken += msg.FuzzyTokens()
	}
	m.logger.Infow("update history with abstract finished",
		"beforeHistory", len(history), "afterHistory", len(newHistory), "afterToken", afterToken)
	return newHistory
}

func theIndexAfterKeep(msgLen int) int {
	mid := msgLen / 2
	keep5 := msgLen - 5
	if keep5 > 0 {
		return keep5
	}
	return mid
}

func remindMessage(noteID string) string {
	return fmt.Sprintf("The original content has been write to the scratchpad, note id: %s. Use tools to retrieve the original text if needed.", noteID)
}

const (
	summaryPrefix = `Several lengthy dialogues have already taken place. The following is a condensed summary of the progress of these historical dialogues:
`
)
