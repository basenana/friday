package contextmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

// SessionMemoryRecord is the on-disk representation of session memory.
// It is stored as session_memory.json alongside the session history.
type SessionMemoryRecord struct {
	GeneratedAt    time.Time `json:"generated_at"`
	LastSyncAt     time.Time `json:"last_sync_at"`
	TaskObjective  string    `json:"task_objective"`
	CurrentStatus  string    `json:"current_status"`
	KeyDecisions   []string  `json:"key_decisions"`
	RecentWork     []string  `json:"recent_work"`
	PendingItems   []string  `json:"pending_items"`
	FileReferences []string  `json:"file_references"`
	ImportantCtx   string    `json:"important_context"`
}

// SessionMemoryStore abstracts reading and writing session memory records.
// Implemented by sessions/file/store.go.
// Implementations must be safe for concurrent reads (the async goroutine reads
// while the main goroutine may also call ensureSnapshots).
type SessionMemoryStore interface {
	WriteSessionMemory(sessionID string, record *SessionMemoryRecord) error
	ReadSessionMemory(sessionID string) (*SessionMemoryRecord, error)
}

// generateSessionMemoryRecord calls the LLM to produce an updated
// SessionMemoryRecord by sending only new messages (msg.Time > syncAfter)
// along with the existing record for context. The LLM returns a complete
// updated record, so no post-hoc merge is needed.
func generateSessionMemoryRecord(ctx context.Context, llm providers.Client, history []types.Message, existingRecord *SessionMemoryRecord, syncAfter time.Time) *SessionMemoryRecord {
	if len(history) == 0 {
		return nil
	}

	// Only summarize messages whose Time is after the last sync boundary.
	var (
		incremental []types.Message
		genAt       time.Time
	)
	for _, msg := range history {
		if msg.Time.After(syncAfter) {
			incremental = append(incremental, msg)
		}
		if msg.Time.After(genAt) {
			genAt = msg.Time
		}
	}
	if len(incremental) == 0 {
		return existingRecord
	}

	prompt := sessionMemoryPrompt(incremental, existingRecord)
	req := providers.NewRequest(prompt)

	var record SessionMemoryRecord
	if err := llm.StructuredPredict(ctx, req, &record); err == nil && !record.IsZero() {
		record.GeneratedAt = genAt
		record.LastSyncAt = lastMessageTime(history)
		return &record
	}

	// Fallback to completion + JSON decode
	resp := llm.Completion(ctx, req)
	msgCh := resp.Message()
	var raw strings.Builder
Wait:
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-resp.Error():
			if err != nil {
				return nil
			}
		case delta, ok := <-msgCh:
			if !ok {
				break Wait
			}
			if delta.Content != "" {
				raw.WriteString(delta.Content)
			}
		}
	}

	if err := decodeSessionMemoryJSON(raw.String(), &record); err != nil {
		return nil
	}
	if record.IsZero() {
		return nil
	}
	record.GeneratedAt = genAt
	record.LastSyncAt = lastMessageTime(history)
	return &record
}

func lastMessageTime(history []types.Message) time.Time {
	for i := len(history) - 1; i >= 0; i-- {
		if !history[i].Time.IsZero() {
			return history[i].Time
		}
	}
	return time.Now()
}

func decodeSessionMemoryJSON(raw string, target *SessionMemoryRecord) error {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < 0 || end <= start {
		return fmt.Errorf("json object not found")
	}
	return json.Unmarshal([]byte(raw[start:end+1]), target)
}

// IsZero returns true if the record has no meaningful content.
func (r *SessionMemoryRecord) IsZero() bool {
	return strings.TrimSpace(r.TaskObjective) == "" &&
		strings.TrimSpace(r.CurrentStatus) == "" &&
		len(r.KeyDecisions) == 0 &&
		len(r.RecentWork) == 0 &&
		len(r.PendingItems) == 0 &&
		len(r.FileReferences) == 0 &&
		strings.TrimSpace(r.ImportantCtx) == ""
}

// ToMessage converts the record to an agent message with <session_memory> XML.
func (r *SessionMemoryRecord) ToMessage() types.Message {
	var builder strings.Builder
	builder.WriteString("<session_memory>\n")
	if r.TaskObjective != "" {
		builder.WriteString("task_objective: ")
		builder.WriteString(r.TaskObjective)
		builder.WriteString("\n")
	}
	if r.CurrentStatus != "" {
		builder.WriteString("current_status: ")
		builder.WriteString(r.CurrentStatus)
		builder.WriteString("\n")
	}
	if len(r.KeyDecisions) > 0 {
		builder.WriteString("key_decisions:\n")
		for _, d := range r.KeyDecisions {
			builder.WriteString("- ")
			builder.WriteString(d)
			builder.WriteString("\n")
		}
	}
	if len(r.RecentWork) > 0 {
		builder.WriteString("recent_work:\n")
		for _, w := range r.RecentWork {
			builder.WriteString("- ")
			builder.WriteString(w)
			builder.WriteString("\n")
		}
	}
	if len(r.PendingItems) > 0 {
		builder.WriteString("pending_items:\n")
		for _, p := range r.PendingItems {
			builder.WriteString("- ")
			builder.WriteString(p)
			builder.WriteString("\n")
		}
	}
	if len(r.FileReferences) > 0 {
		builder.WriteString("file_references: ")
		builder.WriteString(strings.Join(r.FileReferences, ", "))
		builder.WriteString("\n")
	}
	if r.ImportantCtx != "" {
		builder.WriteString("important_context: ")
		builder.WriteString(r.ImportantCtx)
		builder.WriteString("\n")
	}
	builder.WriteString("</session_memory>")
	return types.Message{Role: types.RoleAgent, Content: strings.TrimSpace(builder.String())}
}

func sessionMemoryPrompt(incrementalHistory []types.Message, existingRecord *SessionMemoryRecord) string {
	var existingSection string
	if existingRecord != nil && !existingRecord.IsZero() {
		existingJSON, _ := json.Marshal(existingRecord)
		existingSection = fmt.Sprintf(`Existing session memory (update and extend this):
%s

`, string(existingJSON))
	}

	return fmt.Sprintf(`You are maintaining a running memory of an autonomous agent session.
Return a single JSON object and nothing else.

The JSON schema is:
{
  "task_objective": "string — primary goal of this session",
  "current_status": "string — where things stand now, 1-2 sentences",
  "key_decisions": ["string — important decisions made"],
  "recent_work": ["string — what was done, in chronological order, concise bullets"],
  "pending_items": ["string — concrete next steps or unresolved work"],
  "file_references": ["string — most relevant file paths, max 8"],
  "important_context": "string — critical context that must not be lost (constraints, risks, discoveries)"
}

Rules:
- Return every field. Use "" for unknown strings and [] for unknown lists.
- recent_work should read as a narrative timeline, not just tool names.
- important_context should capture anything that would be confusing to lose.
- Preserve concrete decisions, file paths, and unresolved work items.
- If existing session memory is provided, merge it with the new information.
- Do not include markdown fences.
%s
New conversation history since last sync:
%s`, existingSection, formatHistoryForSessionMemory(incrementalHistory))
}

func formatHistoryForSessionMemory(history []types.Message) string {
	var lines []string
	for i, msg := range history {
		switch msg.Role {
		case types.RoleUser:
			lines = append(lines, fmt.Sprintf("[User %d] %s", i+1, msg.Content))
		case types.RoleAgent:
			lines = append(lines, fmt.Sprintf("[Agent %d] %s", i+1, msg.Content))
		case types.RoleAssistant:
			if msg.Content != "" {
				lines = append(lines, fmt.Sprintf("[Assistant %d] %s", i+1, msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				lines = append(lines, fmt.Sprintf("[ToolCall %d] %s(%s)", i+1, tc.Name, tc.Arguments))
			}
		case types.RoleTool:
			if msg.ToolResult != nil {
				lines = append(lines, fmt.Sprintf("[ToolResult %d] %s", i+1, msg.ToolResult.Content))
			}
		}
	}
	return strings.Join(filterHistoryLines(lines, 3), "\n")
}

func filterHistoryLines(lines []string, minLen int) []string {
	var result []string
	for _, line := range lines {
		if len(line) > minLen {
			result = append(result, line)
		}
	}
	return result
}

// maybeStartAsyncSessionMemory triggers a background goroutine to generate
// session memory when new messages since last sync exceed the token threshold.
// It is non-blocking and uses atomic CAS on SessionMemoryGenerating to prevent concurrent runs.
// Only messages with Time > LastSyncedAt are sent to the LLM (incremental).
func (m *Manager) maybeStartAsyncSessionMemory(sess *session.Session, st *session.ContextState, history []types.Message) {
	if m.llm == nil {
		return
	}

	// Compute tokens of new (unsynced) messages
	var newTokens int64
	for _, msg := range history {
		if msg.Time.After(st.LastSyncedAt) {
			newTokens += msg.FuzzyTokens()
		}
	}
	if newTokens < m.cfg.SessionMemoryThreshold {
		return
	}
	// Atomic CAS to prevent concurrent generation
	if !atomic.CompareAndSwapInt32(&st.SessionMemoryGenerating, 0, 1) {
		return
	}

	historySnap := append([]types.Message(nil), history...)
	syncAfter := st.LastSyncedAt

	isRoot := sess.Root == nil || sess.Root == sess

	// Root: load existing record from store for incremental merge.
	// Fork: nil existing record (generate from incremental history only).
	var existingRecord *SessionMemoryRecord
	if isRoot && m.cfg.SessionMemoryStore != nil {
		if rec, err := m.cfg.SessionMemoryStore.ReadSessionMemory(sess.ID); err == nil && rec != nil {
			existingRecord = rec
		}
	}

	go func() {
		defer atomic.StoreInt32(&st.SessionMemoryGenerating, 0)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		record := generateSessionMemoryRecord(ctx, m.llm, historySnap, existingRecord, syncAfter)
		if record == nil {
			m.logger.Warnw("async session memory generation failed",
				"session", sess.ID,
				"sync_after", syncAfter,
			)
			return
		}

		// Only root sessions persist to store.
		if isRoot && m.cfg.SessionMemoryStore != nil {
			if err := m.cfg.SessionMemoryStore.WriteSessionMemory(sess.ID, record); err != nil {
				m.logger.Errorw("failed to persist session memory",
					"session", sess.ID,
					"error", err,
				)
			}
		}

		// All sessions store in their own ContextState pending slot.
		st.StorePendingMemory(record)

		m.logger.Infow("async session memory generated",
			"session", sess.ID,
			"sync_after", syncAfter,
			"last_sync_at", record.LastSyncAt,
			"task_objective", trimForProjection(record.TaskObjective, 120),
			"pending_items", len(record.PendingItems),
			"file_references", len(record.FileReferences),
		)
	}()
}

// compactWithSessionMemory rebuilds session history by replacing messages before
// LastSyncedAt with the session memory block. It persists the new history via
// ReplaceHistory, similar to compactWithSummary.
func (m *Manager) compactWithSessionMemory(ctx context.Context, sess *session.Session, history []types.Message) error {
	st := sess.EnsureContextState()
	historyTokens := countTokens(history)
	if st.LastSyncedAt.IsZero() || len(st.SessionMemory) == 0 {
		return fmt.Errorf("no session memory available")
	}

	// Collect messages after the sync boundary as the tail.
	var tail []types.Message
	for _, msg := range history {
		if msg.Time.After(st.LastSyncedAt) {
			tail = append(tail, msg)
		}
	}

	tailTokens := countTokens(tail)
	smTokens := countTokens(st.SessionMemory)

	totalProjected := smTokens + tailTokens
	if totalProjected > st.PromptBudget.SoftThreshold {
		return fmt.Errorf("session memory compact exceeds threshold")
	}

	compacted := make([]types.Message, 0, len(st.SessionMemory)+len(tail))
	compacted = append(compacted, cloneMessages(st.SessionMemory)...)
	compacted = append(compacted, cloneMessages(tail)...)

	if err := sess.ReplaceHistory(compacted...); err != nil {
		m.logger.Errorw("failed to persist session memory compacted history", "session", sess.ID, "error", err)
		return err
	}
	st = sess.EnsureContextState()
	st.LastCompactionAt = time.Now()
	st.LastCompactionTokens = historyTokens

	m.logger.Infow("session memory compaction complete",
		"session", sess.ID,
		"history_messages_before", len(history),
		"history_tokens_before", historyTokens,
		"history_messages_after", len(compacted),
		"history_tokens_after", countTokens(compacted),
		"last_synced_at", st.LastSyncedAt,
	)
	return nil
}
