package contextmgr

import (
	stdctx "context"
	"fmt"
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
	defaultMaxToolCallArgsChars   int     = 240
	defaultMaxAssistantChars      int     = 800
	defaultMaxUserChars           int     = 1000
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
	MaxToolCallArgsChars   int
	MaxAssistantChars      int
	MaxUserChars           int
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
	if cfg.MaxToolCallArgsChars == 0 {
		cfg.MaxToolCallArgsChars = defaultMaxToolCallArgsChars
	}
	if cfg.MaxAssistantChars == 0 {
		cfg.MaxAssistantChars = defaultMaxAssistantChars
	}
	if cfg.MaxUserChars == 0 {
		cfg.MaxUserChars = defaultMaxUserChars
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
	history := req.History()
	promptOverhead := session.EstimateRequestOverhead(req)
	inputTokens := countTokens(sess, history) + promptOverhead
	softThresholdExceeded := false
	microcompactUsed := false
	sessionMemoryUsed := false
	hardCompacted := false

	m.logger.Infow("starting context projection",
		"session", sess.ID,
		"history_messages", len(history),
		"history_tokens", inputTokens,
		"session_memory_messages", len(st.SessionMemory),
		"last_synced_at", st.LastSyncedAt,
		"context_window", st.PromptBudget.ContextWindow,
		"soft_threshold", st.PromptBudget.SoftThreshold,
		"hard_threshold", st.PromptBudget.HardThreshold,
	)

	m.ensureSessionMemory(ctx, sess)
	m.maybeStartAsyncSessionMemory(sess, st, history)

	projected := m.projectHistory(sess, sess.ID, history, projectionFull)
	projectedTokens := countTokens(sess, projected) + promptOverhead
	fullProjected := projected
	fullProjectedTokens := projectedTokens

	if projectedTokens > st.PromptBudget.SoftThreshold {
		softThresholdExceeded = true
		m.logger.Warnw("soft threshold exceeded, pruning old messages",
			"session", sess.ID,
			"projected_tokens", projectedTokens,
			"soft_threshold", st.PromptBudget.SoftThreshold,
		)
		microProjected := m.projectHistory(sess, sess.ID, history, projectionMicroCompact)
		microProjectedTokens := countTokens(sess, microProjected) + promptOverhead
		if fullProjectedTokens > 0 && float64(microProjectedTokens)/float64(fullProjectedTokens) < 0.8 {
			projected = microProjected
			projectedTokens = microProjectedTokens
			microcompactUsed = true
			m.logger.Infow("microcompact projection complete",
				"session", sess.ID,
				"projected_tokens", projectedTokens,
				"saved_tokens", fullProjectedTokens-projectedTokens,
			)
		} else {
			projected = fullProjected
			projectedTokens = fullProjectedTokens
			m.logger.Warnw("microcompact was not beneficial, keeping full projection",
				"session", sess.ID,
				"full_projected_tokens", fullProjectedTokens,
				"micro_projected_tokens", microProjectedTokens,
			)
		}
	}

	if projectedTokens > st.PromptBudget.HardThreshold {
		m.logger.Warnw("hard threshold exceeded",
			"session", sess.ID,
			"projected_tokens", projectedTokens,
			"hard_threshold", st.PromptBudget.HardThreshold,
		)

		// Try session memory compact first
		if err := m.compactWithSessionMemory(ctx, sess, history); err == nil {
			hardCompacted = true
			sessionMemoryUsed = true
			st = sess.EnsureContextState()
			history = sess.GetHistory()
			projected = cloneMessages(history)
			projectedTokens = countTokens(sess, projected) + promptOverhead
			m.logger.Infow("session memory compact fits under hard threshold",
				"session", sess.ID,
				"projected_tokens", projectedTokens,
				"last_synced_at", st.LastSyncedAt,
			)
		} else {
			// do durable compact with summary
			if err = m.compactWithSummary(ctx, sess, history); err == nil {
				hardCompacted = true
				st = sess.EnsureContextState()
				history = sess.GetHistory()
				projected = m.projectHistory(sess, sess.ID, history, projectionFull)
				projectedTokens = countTokens(sess, projected) + promptOverhead
			} else {
				m.logger.Errorw("history compaction failed; continuing with best-effort projection",
					"session", sess.ID,
					"error", err,
				)
			}
		}
	}

	req.SetHistory(projected)
	m.logger.Infow("context projection complete",
		"session", sess.ID,
		"projected_messages", len(projected),
		"projected_tokens", projectedTokens,
		"final_mode", finalProjectionMode(microcompactUsed, sessionMemoryUsed, hardCompacted),
		"soft_threshold_exceeded", softThresholdExceeded,
		"microcompact_used", microcompactUsed,
		"session_memory_used", sessionMemoryUsed,
		"hard_compacted", hardCompacted,
	)
	return nil
}

func (m *Manager) AfterTool(_ stdctx.Context, _ *session.Session, _ session.ToolPayload) error {
	return nil
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

type projectionMode int

const (
	projectionFull projectionMode = iota
	projectionMicroCompact
)

func (m projectionMode) String() string {
	switch m {
	case projectionFull:
		return "full"
	case projectionMicroCompact:
		return "microcompact"
	default:
		return "unknown"
	}
}

type conversationGroup struct {
	Messages  []types.Message
	ToolNames []string
	Tokens    int64
}

func (m *Manager) projectHistory(sess *session.Session, sessionID string, history []types.Message, mode projectionMode) []types.Message {
	groups := groupHistory(history)
	tailStart := len(groups) - projectionTailGroups
	if tailStart < 0 {
		tailStart = 0
	}
	oldGroups := groups[:tailStart]
	tailGroups := groups[tailStart:]

	var projected []types.Message
	if mode == projectionMicroCompact {
		projected = m.buildMicroCompactProjection(oldGroups, tailGroups)
	} else {
		projected = append(projected, flattenGroups(oldGroups)...)
		projected = append(projected, flattenGroups(tailGroups)...)
	}

	m.logger.Infow("projection mode built",
		"session", sessionID,
		"mode", mode.String(),
		"input_messages", len(history),
		"groups", len(groups),
		"old_groups", len(oldGroups),
		"tail_groups", len(tailGroups),
		"projected_messages", len(projected),
		"projected_tokens", countTokens(sess, projected),
		"group_tools", strings.Join(groupToolSummary(groups), ","),
	)
	return projected
}

// buildMicroCompactProjection constructs a projection that prunes old messages
// by trimming content lengths while preserving tail groups unchanged.
func (m *Manager) buildMicroCompactProjection(oldGroups, tailGroups []conversationGroup) []types.Message {
	var pruned []types.Message
	for _, group := range oldGroups {
		for _, msg := range group.Messages {
			pruned = append(pruned, pruneMessage(msg, m.cfg))
		}
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
	case types.RoleAssistant:
		if len(msg.ToolCalls) > 0 {
			msg.ToolCalls = pruneToolCalls(msg.ToolCalls, cfg)
		}
		msg.Content = trimForProjection(msg.Content, cfg.MaxAssistantChars)
		msg.Reasoning = ""
	case types.RoleUser:
		msg.Content = trimForProjection(msg.Content, cfg.MaxUserChars)
	}
	if !reflect.DeepEqual(original, msg) {
		msg.Tokens = 0
	}
	return msg
}

func pruneToolCalls(calls []types.ToolCall, cfg Config) []types.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	pruned := make([]types.ToolCall, 0, len(calls))
	for _, call := range calls {
		pruned = append(pruned, pruneToolCall(call, cfg))
	}
	return pruned
}

func pruneToolCall(call types.ToolCall, cfg Config) types.ToolCall {
	if !needsProjectionTrim(call.Arguments, cfg.MaxToolCallArgsChars) {
		return call
	}
	cp := call
	cp.Arguments = trimForProjection(call.Arguments, cfg.MaxToolCallArgsChars)
	return cp
}

func cloneMessages(msgs []types.Message) []types.Message {
	return append([]types.Message(nil), msgs...)
}

func trimForProjection(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func needsProjectionTrim(text string, limit int) bool {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	return len([]rune(text)) > limit
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

func (g conversationGroup) primaryToolName() string {
	if len(g.ToolNames) == 0 {
		return ""
	}
	return g.ToolNames[0]
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func finalProjectionMode(microcompactUsed, sessionMemoryUsed, hardCompacted bool) string {
	if sessionMemoryUsed {
		return "session_memory_boundary"
	}
	if hardCompacted {
		return "full_after_compact"
	}
	if microcompactUsed {
		return "microcompact"
	}
	return "full"
}

func groupToolSummary(groups []conversationGroup) []string {
	var summary []string
	for _, group := range groups {
		tool := group.primaryToolName()
		if tool == "" {
			continue
		}
		entry := fmt.Sprintf("%s:%dt", tool, group.Tokens)
		if containsString(summary, entry) {
			continue
		}
		summary = append(summary, entry)
		if len(summary) >= 6 {
			break
		}
	}
	return summary
}
