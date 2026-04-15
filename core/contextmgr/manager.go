package contextmgr

import (
	stdctx "context"
	"reflect"
	"strings"
	"time"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

const (
	defaultContextWindow          int64   = 128 * 1000
	defaultSoftThresholdRatio     float64 = 0.70
	defaultHardThresholdRatio     float64 = 0.85
	defaultCompletionRatio        float64 = 0.18
	defaultToolReserveRatio       float64 = 0.07
	defaultMaxToolResultChars     int     = 600
	defaultSessionMemoryThreshold int64   = 15_000
	projectionTailGroups          int     = 4
)

type Config struct {
	ContextWindow          int64
	SoftThresholdRatio     float64
	HardThresholdRatio     float64
	CompletionRatio        float64
	ToolReserveRatio       float64
	MaxToolResultChars     int
	SessionMemoryThreshold int64

	SessionMemoryStore SessionMemoryStore
}

type Manager struct {
	llm    providers.Client
	cfg    Config
	logger logger.Logger
}

func New(llm providers.Client, cfg Config) *Manager {
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = defaultContextWindow
	}
	if cfg.SoftThresholdRatio == 0 {
		cfg.SoftThresholdRatio = defaultSoftThresholdRatio
	}
	if cfg.HardThresholdRatio == 0 {
		cfg.HardThresholdRatio = defaultHardThresholdRatio
	}
	if cfg.CompletionRatio == 0 {
		cfg.CompletionRatio = defaultCompletionRatio
	}
	if cfg.ToolReserveRatio == 0 {
		cfg.ToolReserveRatio = defaultToolReserveRatio
	}
	if cfg.MaxToolResultChars == 0 {
		cfg.MaxToolResultChars = defaultMaxToolResultChars
	}
	if cfg.SessionMemoryThreshold == 0 {
		cfg.SessionMemoryThreshold = defaultSessionMemoryThreshold
	}

	return &Manager{
		llm:    llm,
		cfg:    cfg,
		logger: logger.New("contextmgr"),
	}
}

func (m *Manager) BeforeModel(ctx stdctx.Context, sess *session.Session, req providers.Request) error {
	st := sess.EnsureContextState()
	st.PromptBudget = m.buildBudget()
	if req.PromptCacheKey() == "" {
		req.SetPromptCacheKey(promptCacheKeyForSession(sess))
	}

	history := req.History()
	promptOverhead := session.EstimateRequestOverhead(req)
	budget := st.PromptBudget

	m.logger.Infow("starting context projection",
		"session", sess.ID,
		"history_messages", len(history),
		"history_tokens", countTokens(sess, history),
		"session_memory_messages", len(st.SessionMemory),
		"last_synced_at", st.LastSyncedAt,
		"context_window", budget.ContextWindow,
		"soft_threshold", budget.SoftThreshold,
		"hard_threshold", budget.HardThreshold,
	)

	m.ensureSessionMemory(ctx, sess)
	m.maybeStartAsyncSessionMemory(sess, st, history)

	projected := history
	projectedTokens := countTokens(sess, history) + promptOverhead

	if projectedTokens > budget.SoftThreshold {
		projected, projectedTokens = m.applyMicroCompact(sess, sess.ID, st, history, projectedTokens, promptOverhead)
	}

	if projectedTokens > budget.HardThreshold {
		projected, projectedTokens = m.applyHardCompact(ctx, sess, history, promptOverhead)
	}

	req.SetHistory(projected)
	m.logger.Infow("context projection complete",
		"session", sess.ID,
		"projected_messages", len(projected),
		"projected_tokens", projectedTokens,
	)
	return nil
}

func (m *Manager) applyMicroCompact(sess *session.Session, sessionID string, st *session.ContextState, history []types.Message, fullTokens int64, promptOverhead int64) ([]types.Message, int64) {
	micro := m.buildMicroProjected(sessionID, st, history)
	microTokens := countTokens(sess, micro) + promptOverhead

	if fullTokens > 0 && float64(microTokens)/float64(fullTokens) < 0.8 {
		m.logger.Infow("microcompact applied",
			"session", sessionID,
			"projected_tokens", microTokens,
			"saved_tokens", fullTokens-microTokens,
		)
		return micro, microTokens
	}

	m.logger.Warnw("microcompact not beneficial, keeping full projection",
		"session", sessionID,
		"full_tokens", fullTokens,
		"micro_tokens", microTokens,
	)
	return history, fullTokens
}

func (m *Manager) buildMicroProjected(sessionID string, st *session.ContextState, history []types.Message) []types.Message {
	if projected, ok := frozenMicroCompactProjection(st, history); ok {
		m.logger.Infow("using frozen microcompact",
			"session", sessionID,
			"projected_messages", len(projected),
			"projected_tokens", countTokens(nil, projected),
		)
		return projected
	}

	groups := groupHistory(history)
	tailStart := len(groups) - projectionTailGroups
	if tailStart < 0 {
		tailStart = 0
	}

	return m.buildMicroCompactProjection(st, groups[:tailStart], groups[tailStart:])
}

func (m *Manager) applyHardCompact(ctx stdctx.Context, sess *session.Session, history []types.Message, promptOverhead int64) ([]types.Message, int64) {
	m.logger.Warnw("hard threshold exceeded",
		"session", sess.ID,
		"hard_threshold", sess.EnsureContextState().PromptBudget.HardThreshold,
	)

	if err := m.compactWithSessionMemory(ctx, sess, history); err == nil {
		projected := sess.GetHistory()
		tokens := countTokens(sess, projected) + promptOverhead
		m.logger.Infow("session memory compact applied",
			"session", sess.ID,
			"projected_tokens", tokens,
		)
		return projected, tokens
	}

	if err := m.compactWithSummary(ctx, sess, history); err == nil {
		projected := sess.GetHistory()
		tokens := countTokens(sess, projected) + promptOverhead
		m.logger.Infow("summary compact applied",
			"session", sess.ID,
			"projected_tokens", tokens,
		)
		return projected, tokens
	}

	m.logger.Errorw("all compaction methods failed",
		"session", sess.ID,
	)
	return history, countTokens(sess, history) + promptOverhead
}

func (m *Manager) ensureSessionMemory(_ stdctx.Context, sess *session.Session) {
	st := sess.EnsureContextState()

	// All sessions (root and forked) drain their own pending record.
	if v := st.DrainPendingMemory(); v != nil {
		record := v.(*SessionMemoryRecord)
		st.SessionMemory = []types.Message{record.ToMessage()}
		st.LastSyncedAt = record.LastSyncAt

		m.logger.Infow("drained pending session memory into context state",
			"session", sess.ID,
			"last_sync_at", record.LastSyncAt,
			"session_memory_tokens", countTokens(sess, st.SessionMemory),
		)

		return
	}

	if m.cfg.SessionMemoryStore == nil {
		return
	}

	// Session memory is only persisted for root sessions; forked sessions keep
	// it in memory only (inherited from cloneContextState at Fork() time).
	if sess.Root != nil && sess.Root.ID != sess.ID {
		return
	}

	prevSyncedAt := st.LastSyncedAt
	record, err := m.cfg.SessionMemoryStore.ReadSessionMemory(sess.ID)
	if err != nil {
		m.logger.Warnw("failed to load session memory from outside",
			"session", sess.ID,
			"error", err,
		)
		return
	}
	if record == nil {
		return
	}

	if record.LastSyncAt.After(prevSyncedAt) {
		m.logger.Infow("loaded session memory from outside",
			"session", sess.ID,
			"last_sync_at", record.LastSyncAt,
			"tokens", countTokens(sess, st.SessionMemory),
		)

		st.SessionMemory = []types.Message{record.ToMessage()}
		st.LastSyncedAt = record.LastSyncAt
	}
	return
}

func (m *Manager) compactWithSummary(ctx stdctx.Context, sess *session.Session, history []types.Message) error {
	st := sess.EnsureContextState()
	historyTokens := countTokens(sess, history)
	m.logger.Infow("starting history compaction",
		"session", sess.ID,
		"history_messages", len(history),
		"history_tokens", historyTokens,
	)

	summary, err := session.SummarizeToCompactSummary(ctx, m.llm, history)
	if err != nil {
		m.logger.Errorw("failed to summarize history into compact summary", "session", sess.ID, "error", err)
		return err
	}

	var compacted []types.Message
	if strings.TrimSpace(summary) == "" {
		m.logger.Warnw("LLM summary empty, falling back to keeping last messages",
			"session", sess.ID,
			"keep", session.CompactFallbackKeepMessages(),
		)
		compacted = session.FallbackCompactHistory(history, session.CompactFallbackKeepMessages())
	} else {
		m.logger.Infow("compact summary ready",
			"session", sess.ID,
			"summary_preview", trimForProjection(summary, 240),
		)
		compacted = session.BuildCompactSummaryHistory(history, summary)
	}

	if err := sess.ReplaceHistory(compacted...); err != nil {
		m.logger.Errorw("failed to persist compacted history", "session", sess.ID, "error", err)
		return err
	}
	st = sess.EnsureContextState()
	st.LastCompactionAt = time.Now()
	st.LastCompactionTokens = historyTokens
	st.ResetMicroCompact()

	m.logger.Infow("history compaction complete",
		"session", sess.ID,
		"history_messages_before", len(history),
		"history_tokens_before", historyTokens,
		"history_messages_after", len(compacted),
		"history_tokens_after", countTokens(sess, compacted),
		"summary_preview", trimForProjection(summary, 240),
	)
	return nil
}

func (m *Manager) buildBudget() session.PromptBudget {
	window := m.cfg.ContextWindow
	if provider, ok := m.llm.(providers.ContextWindowProvider); ok && provider.ContextWindow() > 0 {
		window = provider.ContextWindow()
	}
	return session.PromptBudget{
		ContextWindow:     window,
		CompletionReserve: int64(float64(window) * m.cfg.CompletionRatio),
		ToolReserve:       int64(float64(window) * m.cfg.ToolReserveRatio),
		SoftThreshold:     int64(float64(window) * m.cfg.SoftThresholdRatio),
		HardThreshold:     int64(float64(window) * m.cfg.HardThresholdRatio),
		TailTarget:        maxInt64(window/5, 8*1024),
	}
}

type conversationGroup struct {
	Messages  []types.Message
	ToolNames []string
	Tokens    int64
}

func (m *Manager) buildMicroCompactProjection(st *session.ContextState, oldGroups, tailGroups []conversationGroup) []types.Message {
	var pruned []types.Message
	for _, group := range oldGroups {
		for _, msg := range group.Messages {
			pruned = append(pruned, pruneMessage(msg, m.cfg))
		}
	}
	pruned = cloneMessages(pruned)
	if st != nil {
		st.MicroCompactPrefix = pruned
		st.MicroCompactSourceMessages = len(flattenGroups(oldGroups))
	}
	return append(pruned, flattenGroups(tailGroups)...)
}

func flattenGroups(groups []conversationGroup) []types.Message {
	var msgs []types.Message
	for _, g := range groups {
		msgs = append(msgs, g.Messages...)
	}
	return msgs
}

func pruneMessage(msg types.Message, cfg Config) types.Message {
	original := msg
	switch msg.Role {
	case types.RoleTool:
		if msg.ToolResult != nil {
			cpMsg := msg
			toolResult := *msg.ToolResult
			toolResult.Content = trimForProjection(toolResult.Content, cfg.MaxToolResultChars)
			cpMsg.ToolResult = &toolResult
			msg = cpMsg
		}
	}
	if !reflect.DeepEqual(original, msg) {
		msg.Tokens = 0
	}
	return msg
}

func cloneMessages(msgs []types.Message) []types.Message {
	return append([]types.Message(nil), msgs...)
}

func trimForProjection(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func countTokens(sess *session.Session, history []types.Message) int64 {
	if sess != nil {
		return sess.CountTokens(history)
	}

	return session.CalibratedTokenCount(history, 1)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func groupHistory(history []types.Message) []conversationGroup {
	if len(history) == 0 {
		return nil
	}

	groups := make([]conversationGroup, 0, len(history))
	current := conversationGroup{}
	flush := func() {
		if len(current.Messages) == 0 {
			return
		}
		groups = append(groups, current)
		current = conversationGroup{}
	}

	for _, msg := range history {
		if shouldStartNewGroup(current, msg) {
			flush()
		}
		current.Messages = append(current.Messages, msg)
		current.Tokens += msg.FuzzyTokens()
		for _, call := range msg.ToolCalls {
			if call.Name != "" && !containsString(current.ToolNames, call.Name) {
				current.ToolNames = append(current.ToolNames, call.Name)
			}
		}
	}
	flush()
	return groups
}

func shouldStartNewGroup(current conversationGroup, next types.Message) bool {
	if len(current.Messages) == 0 {
		return false
	}
	if next.Role == types.RoleUser {
		return true
	}
	if next.Role == types.RoleAssistant && len(next.ToolCalls) > 0 && current.hasNonUserActivity() {
		return true
	}
	return false
}

func (g conversationGroup) hasNonUserActivity() bool {
	for _, msg := range g.Messages {
		if msg.Role != types.RoleUser {
			return true
		}
	}
	return false
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func frozenMicroCompactProjection(st *session.ContextState, history []types.Message) ([]types.Message, bool) {
	if st == nil || st.MicroCompactSourceMessages == 0 || len(st.MicroCompactPrefix) == 0 {
		return nil, false
	}
	if len(history) < st.MicroCompactSourceMessages {
		st.ResetMicroCompact()
		return nil, false
	}

	afterMessage := cloneMessages(history[st.MicroCompactSourceMessages:])
	if countTokens(nil, afterMessage) > defaultSessionMemoryThreshold*2 {
		st.ResetMicroCompact()
		return nil, false
	}

	projected := make([]types.Message, 0, len(st.MicroCompactPrefix)+(len(history)-st.MicroCompactSourceMessages))
	projected = append(projected, cloneMessages(st.MicroCompactPrefix)...)
	projected = append(projected, afterMessage...)
	return projected, true
}

func promptCacheKeyForSession(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	root := sess.Root
	if root == nil {
		root = sess
	}
	if root.ID == "" {
		return ""
	}
	return "session:" + root.ID
}
