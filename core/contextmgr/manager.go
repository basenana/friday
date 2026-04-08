package contextmgr

import (
	stdctx "context"
	"strings"
	"time"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

const (
	defaultContextWindow        int64   = 128 * 1000
	defaultSoftThresholdRatio   float64 = 0.70
	defaultHardThresholdRatio   float64 = 0.85
	defaultCompletionRatio      float64 = 0.18
	defaultToolReserveRatio     float64 = 0.07
	defaultPreserveTailMessages int     = 8
	defaultMaxToolResultChars   int     = 600
	defaultMaxAssistantChars    int     = 800
	defaultMaxUserChars         int     = 1000
)

type MemoryBridge interface {
	LoadSessionMemory(ctx stdctx.Context, sess *session.Session) ([]types.Message, error)
}

type Config struct {
	ContextWindow        int64
	SoftThresholdRatio   float64
	HardThresholdRatio   float64
	CompletionRatio      float64
	ToolReserveRatio     float64
	PreserveTailMessages int
	MaxToolResultChars   int
	MaxAssistantChars    int
	MaxUserChars         int

	MemoryBridge MemoryBridge
}

type Manager struct {
	llm providers.Client
	cfg Config

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
	if cfg.PreserveTailMessages == 0 {
		cfg.PreserveTailMessages = defaultPreserveTailMessages
	}
	if cfg.MaxToolResultChars == 0 {
		cfg.MaxToolResultChars = defaultMaxToolResultChars
	}
	if cfg.MaxAssistantChars == 0 {
		cfg.MaxAssistantChars = defaultMaxAssistantChars
	}
	if cfg.MaxUserChars == 0 {
		cfg.MaxUserChars = defaultMaxUserChars
	}

	return &Manager{
		llm:    llm,
		cfg:    cfg,
		logger: logger.New("contextmgr"),
	}
}

func (m *Manager) BeforeModel(ctx stdctx.Context, sess *session.Session, req providers.Request) error {
	// Memory snapshot is loaded lazily and cached in ContextState.
	if err := m.ensureSnapshots(ctx, sess); err != nil {
		return err
	}

	st := sess.EnsureContextState()
	st.PromptBudget = m.buildBudget()
	history := req.History()
	cf := st.CaseFile
	inputTokens := countTokens(history)
	softPruned := false
	hardCompacted := false

	m.logger.Infow("starting context projection",
		"session", sess.ID,
		"history_messages", len(history),
		"history_tokens", inputTokens,
		"memory_messages", len(st.MemorySnapshot),
		"has_case_file", !cf.IsZero(),
		"soft_threshold", st.PromptBudget.SoftThreshold,
		"hard_threshold", st.PromptBudget.HardThreshold,
	)

	// Default projection keeps the full non-synthetic history untouched.
	projected := m.projectHistory(history, st, cf, false)
	projectedTokens := countTokens(projected)
	if projectedTokens > st.PromptBudget.SoftThreshold {
		softPruned = true
		m.logger.Warnw("soft threshold exceeded, pruning old messages",
			"session", sess.ID,
			"projected_tokens", projectedTokens,
			"soft_threshold", st.PromptBudget.SoftThreshold,
			"preserve_tail_messages", m.cfg.PreserveTailMessages,
		)
		// Soft compact only trims older messages; it does not drop them entirely.
		projected = m.projectHistory(history, st, cf, true)
		projectedTokens = countTokens(projected)
	}

	if projectedTokens > st.PromptBudget.HardThreshold {
		hardCompacted = true
		m.logger.Warnw("hard threshold exceeded, compacting history",
			"session", sess.ID,
			"projected_tokens", projectedTokens,
			"hard_threshold", st.PromptBudget.HardThreshold,
			"history_messages", len(history),
			"history_tokens", inputTokens,
		)
		// Hard threshold means projection alone is not enough; compact the persisted
		// session history into a durable case file, then rebuild the request from that.
		if err := m.compact(ctx, sess, history); err != nil {
			m.logger.Errorw("history compaction failed", "session", sess.ID, "error", err)
			return err
		}
		st = sess.EnsureContextState()
		history = sess.GetHistory()
		cf = st.CaseFile
		projected = m.projectHistory(history, st, cf, false)
		projectedTokens = countTokens(projected)
	}

	req.SetHistory(projected)
	m.logger.Infow("context projection complete",
		"session", sess.ID,
		"projected_messages", len(projected),
		"projected_tokens", projectedTokens,
		"soft_pruned", softPruned,
		"hard_compacted", hardCompacted,
		"has_case_file", !cf.IsZero(),
	)
	return nil
}

func (m *Manager) AfterTool(_ stdctx.Context, sess *session.Session, payload session.ToolPayload) error {
	st := sess.EnsureContextState()

	var observations []session.ToolObservation
	for _, execution := range payload.Executions {
		name := execution.Call.Name
		if name == "" {
			name = "unknown_tool"
		}

		var (
			content string
			success = true
			files   []session.FileRef
		)

		for _, msg := range execution.Messages {
			files = append(files, collectMessageFileRefs(msg)...)
			if msg.Role == types.RoleTool && msg.ToolResult != nil {
				content = msg.ToolResult.Content
				if !msg.ToolResult.Success {
					success = false
				}
			}
		}
		if strings.TrimSpace(content) == "" {
			continue
		}

		obs := session.ToolObservation{
			ToolName:   name,
			Summary:    trimForProjection(content, m.cfg.MaxToolResultChars),
			Success:    success,
			ObservedAt: time.Now(),
		}
		for _, file := range files {
			obs.Files = append(obs.Files, file.Path)
		}
		observations = append(observations, obs)
	}

	st.ToolObservations = mergeToolObservations(st.ToolObservations, observations)
	if len(payload.Executions) > 0 {
		m.logger.Infow("recorded tool observations",
			"session", sess.ID,
			"executions", len(payload.Executions),
			"new_observations", len(observations),
			"total_observations", len(st.ToolObservations),
		)
	}
	return nil
}

func (m *Manager) ensureSnapshots(ctx stdctx.Context, sess *session.Session) error {
	st := sess.EnsureContextState()
	if !st.MemoryLoaded && m.cfg.MemoryBridge != nil {
		messages, err := m.cfg.MemoryBridge.LoadSessionMemory(ctx, sess)
		if err != nil {
			m.logger.Errorw("failed to load memory snapshot", "session", sess.ID, "error", err)
			return err
		}
		st.MemorySnapshot = append([]types.Message(nil), messages...)
		st.MemoryLoaded = true
		m.logger.Infow("loaded memory snapshot",
			"session", sess.ID,
			"messages", len(messages),
			"tokens", countTokens(messages),
		)
	}
	return nil
}

func (m *Manager) compact(ctx stdctx.Context, sess *session.Session, history []types.Message) error {
	st := sess.EnsureContextState()
	historyTokens := countTokens(history)
	m.logger.Infow("starting history compaction",
		"session", sess.ID,
		"history_messages", len(history),
		"history_tokens", historyTokens,
		"has_existing_case_file", !st.CaseFile.IsZero(),
	)

	summary, err := session.SummarizeToCaseFile(ctx, m.llm, st.CaseFile, history)
	if err != nil {
		m.logger.Errorw("failed to summarize history into case file", "session", sess.ID, "error", err)
		return err
	}
	st.CaseFile = session.MergeCaseFiles(st.CaseFile, summary)
	st.LastCompactionAt = time.Now()
	st.LastCompactionTokens = historyTokens

	compacted := session.BuildCompactedHistory(history, st.CaseFile, m.cfg.PreserveTailMessages)
	if err := sess.ReplaceHistory(compacted...); err != nil {
		m.logger.Errorw("failed to persist compacted history", "session", sess.ID, "error", err)
		return err
	}

	m.logger.Infow("history compaction complete",
		"session", sess.ID,
		"history_messages_before", len(history),
		"history_tokens_before", historyTokens,
		"history_messages_after", len(compacted),
		"history_tokens_after", countTokens(compacted),
		"pending_work", len(st.CaseFile.PendingWork),
		"recent_files", len(st.CaseFile.RecentFiles),
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

func (m *Manager) projectHistory(history []types.Message, st *session.ContextState, cf session.CaseFile, pruneOld bool) []types.Message {
	clean := make([]types.Message, 0, len(history))
	for _, msg := range history {
		if session.IsSyntheticContextMessage(msg) {
			continue
		}
		clean = append(clean, msg)
	}

	tailStart := len(clean) - m.cfg.PreserveTailMessages
	if tailStart < 0 {
		tailStart = 0
	}

	projected := make([]types.Message, 0, len(st.MemorySnapshot)+len(clean)+1)
	projected = append(projected, cloneMessages(st.MemorySnapshot)...)
	if !cf.IsZero() {
		projected = append(projected, cf.ToMessage())
	}

	for idx, msg := range clean {
		if pruneOld && idx < tailStart {
			projected = append(projected, pruneMessage(msg, m.cfg))
			continue
		}

		projected = append(projected, msg)
	}
	return projected
}

func pruneMessage(msg types.Message, cfg Config) types.Message {
	switch msg.Role {
	case types.RoleTool:
		if msg.ToolResult != nil {
			cpMsg := msg
			toolResult := *msg.ToolResult
			toolResult.Content = trimForProjection(toolResult.Content, cfg.MaxToolResultChars)
			cpMsg.ToolResult = &toolResult
			return cpMsg
		}
	case types.RoleAssistant:
		if len(msg.ToolCalls) == 0 {
			msg.Content = trimForProjection(msg.Content, cfg.MaxAssistantChars)
		}
		msg.Reasoning = ""
	case types.RoleUser, types.RoleAgent:
		msg.Content = trimForProjection(msg.Content, cfg.MaxUserChars)
	}
	return msg
}

func collectMessageFileRefs(msg types.Message) []session.FileRef {
	var refs []session.FileRef
	refs = append(refs, session.ExtractFileRefs(msg.GetContent(), string(msg.Role), msg.Time)...)
	for _, call := range msg.ToolCalls {
		refs = append(refs, session.ExtractFileRefs(call.Arguments, call.Name, msg.Time)...)
	}
	return refs
}

func mergeToolObservations(existing, extra []session.ToolObservation) []session.ToolObservation {
	if len(extra) == 0 {
		return existing
	}
	result := append(append([]session.ToolObservation(nil), existing...), extra...)
	if len(result) > 12 {
		result = result[len(result)-12:]
	}
	return result
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

func countTokens(history []types.Message) int64 {
	var total int64
	for _, msg := range history {
		total += msg.FuzzyTokens()
	}
	return total
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
