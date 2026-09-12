package contextmgr

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
)

const (
	refocusToolName         = "refocus_objectives"
	refocusMinHistoryTokens = int64(30_000)
	refocusSummaryMaxChars  = 8_000
	refocusTailMessages     = 6
)

const refocusSystemPrompt = `## Context Refocus Tool

You have access to the ` + "`refocus_objectives`" + ` tool, which proactively compacts the conversation history.

STANDING CHECK — when a new user message arrives: before starting work, assess whether the conversation history still serves the current task. If large parts of it have become stale — superseded decisions, abandoned or explicitly voided topics, long exploratory detours the user has moved past — call ` + "`refocus_objectives`" + ` FIRST, before any other action. Refocusing reclaims context tokens and sharpens attention on the current objective.

Strong signals that a refocus is warranted:
- The user explicitly cancels, voids, or discards earlier discussion ("these are no longer needed", "forget all that").
- The user announces a brand-new task unrelated to what came before.
- The conversation has drifted across several unrelated topics while the current task only depends on the latest one.

Do NOT use it when the history is still short (the tool silently no-ops below ~30k tokens), or when the history contains critical information you cannot fully restate in the summary — anything not captured in your summary is permanently discarded.`

const refocusToolDescription = `Proactively compact the conversation context and refocus on the current objective.

WHEN TO USE:
- The conversation history has accumulated lots of content unrelated to the current task.
- The discussion drifted across multiple topics and earlier details no longer matter.
- You want to reclaim context tokens before automatic threshold-based compaction kicks in.

HOW IT WORKS:
After this tool returns, the full history will be replaced — before the next model call — by: the latest session memory, the summary you provide, and the most recent messages. Everything else is permanently discarded. The tool no-ops (returns success without effect) when the history is below ~30k tokens.

The "summary" argument is REQUIRED and MUST follow this markdown template:

## Current Objective
<one paragraph: what the current task is, and why>

## Progress So Far
<bullets: concrete progress — what was done, key outputs, files created/modified with their paths>

## Key Facts & Decisions
<bullets: constraints, decisions, discoveries, important identifiers or error fixes that must not be lost>

## Next Steps
<bullets: the concrete remaining actions to finish the task>

Write the summary carefully: it becomes the anchor of the compacted context. Keep it under 8000 characters.`

const refocusAppliedMessage = "Context refocus applied. The conversation history will be compacted before the next model call using the latest session memory plus your summary. Continue with the current task."

// Refocus exposes proactive history compaction to the agent and applies its
// summary on the following model loop through Manager.
type Refocus struct {
	logger logger.Logger
}

var _ session.BeforeAgentHook = &Refocus{}
var _ session.BeforeModelHook = &Refocus{}

func NewRefocusHook() *Refocus {
	return &Refocus{logger: logger.New("contextmgr.refocus")}
}

func (r *Refocus) BeforeAgent(_ context.Context, sess *session.Session, req session.AgentRequest) error {
	if req != nil {
		req.AppendTools(r.refocusTool(sess))
	}
	return nil
}

func (r *Refocus) BeforeModel(_ context.Context, _ *session.Session, req providers.Request) error {
	req.AppendSystemPrompt(refocusSystemPrompt)
	return nil
}

func (r *Refocus) refocusTool(sess *session.Session) *tools.Tool {
	return tools.NewTool(
		refocusToolName,
		tools.WithDescription(refocusToolDescription),
		tools.WithString("summary",
			tools.Required(),
			tools.Description("Markdown summary following the template in the tool description (Current Objective / Progress So Far / Key Facts & Decisions / Next Steps)."),
		),
		tools.WithToolHandler(r.handler(sess)),
	)
}

func (r *Refocus) handler(sess *session.Session) tools.ToolHandlerFunc {
	return func(_ context.Context, request *tools.Request) (*tools.Result, error) {
		summary, _ := request.Arguments["summary"].(string)
		if strings.TrimSpace(summary) == "" {
			return tools.NewToolResultError("summary is required and must be non-empty"), nil
		}
		if utf8.RuneCountInString(summary) > refocusSummaryMaxChars {
			return tools.NewToolResultError(fmt.Sprintf("summary exceeds %d characters", refocusSummaryMaxChars)), nil
		}
		if sess.Tokens() < refocusMinHistoryTokens {
			return tools.NewToolResultText(refocusAppliedMessage), nil
		}
		sess.EnsureContextState().StorePendingRefocus(summary)
		r.logger.Infow("refocus scheduled",
			"session", sess.ID,
			"history_tokens", sess.Tokens(),
			"summary_chars", utf8.RuneCountInString(summary),
		)
		return tools.NewToolResultText(refocusAppliedMessage), nil
	}
}

func buildRefocusMessage(summary string) types.Message {
	return types.Message{
		Role:    types.RoleAgent,
		Content: "<refocus_summary>\n" + strings.TrimSpace(summary) + "\n</refocus_summary>",
	}
}

func (m *Manager) applyRefocusCompact(ctx context.Context, sess *session.Session, refocusSummary string) ([]types.Message, int64) {
	ctx, span := tracing.Start(ctx, "contextmgr.refocus_compact",
		tracing.WithAttributes(tracing.String("session.id", sess.ID)),
	)
	defer span.End()

	st := sess.EnsureContextState()
	history := sess.GetHistory()
	historyTokens := countTokens(sess, history)
	startedAt := time.Now()

	sess.PublishEvent(types.Event{
		Type: types.EventCompactStart,
		Data: map[string]string{
			"history_len": strconv.Itoa(len(history)),
			"trigger":     string(session.CompactTriggerRefocus),
		},
	})

	var prefix []types.Message
	if len(st.SessionMemory) > 0 {
		prefix = append(prefix, cloneMessages(st.SessionMemory)...)
	}
	prefix = append(prefix, buildRefocusMessage(refocusSummary))

	var tail []types.Message
	if !st.LastSyncedAt.IsZero() {
		for _, msg := range history {
			if msg.Time.After(st.LastSyncedAt) {
				tail = append(tail, msg)
			}
		}
	}
	if len(tail) == 0 {
		tail = session.FallbackCompactHistory(history, refocusTailMessages)
	}

	compacted := append(prefix, cloneMessages(tail)...)
	if err := sess.ReplaceHistory(compacted...); err != nil {
		m.logger.Errorw("failed to persist refocus compacted history", "session", sess.ID, "error", err)
		span.RecordError(err)
		sess.PublishEvent(types.Event{
			Type: types.EventCompactFinish,
			Data: session.CompactFailData("refocus", session.CompactTriggerRefocus, historyTokens, time.Since(startedAt), err),
		})
		fallback := sess.GetHistory()
		return fallback, countTokens(sess, fallback)
	}

	st = sess.EnsureContextState()
	st.LastCompactionAt = time.Now()
	st.LastCompactionTokens = historyTokens
	st.TokenCheckpoint = session.TokenCheckpoint{}
	st.ResetMicroCompact()

	tokensAfter := countTokens(sess, compacted)
	sess.PublishEvent(types.Event{
		Type: types.EventCompactFinish,
		Data: session.CompactFinishData("refocus", session.CompactTriggerRefocus, historyTokens, tokensAfter, time.Since(startedAt)),
	})
	span.AddEvent("refocus_compact_applied",
		tracing.Int("tokens_before", historyTokens),
		tracing.Int("tokens_after", tokensAfter),
	)
	m.logger.Infow("refocus compaction complete",
		"session", sess.ID,
		"history_messages_before", len(history),
		"history_tokens_before", historyTokens,
		"history_messages_after", len(compacted),
		"history_tokens_after", tokensAfter,
	)
	return sess.GetHistory(), tokensAfter
}
