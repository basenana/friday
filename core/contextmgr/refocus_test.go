package contextmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type failingRefocusWriter struct{ err error }

func (w failingRefocusWriter) AppendMessages(string, ...types.Message) error { return nil }
func (w failingRefocusWriter) ReplaceMessages(string, ...types.Message) error {
	return w.err
}

func newRefocusSession(id string, historyTokens int64) *session.Session {
	return session.New(id, nil, session.WithHistory(
		types.Message{Role: types.RoleUser, Content: "large history", Tokens: historyTokens},
	))
}

func refocusToolForTest(t *testing.T, hook *Refocus, sess *session.Session) *tools.Tool {
	t.Helper()
	req := &api.Request{}
	if err := hook.BeforeAgent(context.Background(), sess, req); err != nil {
		t.Fatalf("BeforeAgent() error = %v", err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != refocusToolName {
		t.Fatalf("unexpected injected tools: %#v", req.Tools)
	}
	return req.Tools[0]
}

func TestRefocusHookInjectsToolAndPrompt(t *testing.T) {
	hook := NewRefocusHook()
	sess := newRefocusSession("refocus-hook", 0)
	refocusToolForTest(t, hook, sess)

	req := providers.NewRequest("")
	if err := hook.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel() error = %v", err)
	}
	if !strings.Contains(req.SystemPrompt(), refocusToolName) {
		t.Fatalf("system prompt does not mention %q", refocusToolName)
	}
}

func TestRefocusToolStoresOrSkipsSummary(t *testing.T) {
	hook := NewRefocusHook()

	large := newRefocusSession("refocus-large", refocusMinHistoryTokens+1)
	tool := refocusToolForTest(t, hook, large)
	result, err := tool.Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{
		"summary": "## Current Objective\nfinish the migration",
	}})
	if err != nil || result.IsError {
		t.Fatalf("large-history refocus failed: result=%#v err=%v", result, err)
	}
	if got := large.EnsureContextState().DrainPendingRefocus(); !strings.Contains(got, "finish the migration") {
		t.Fatalf("pending summary = %q", got)
	}

	small := newRefocusSession("refocus-small", refocusMinHistoryTokens-1)
	tool = refocusToolForTest(t, hook, small)
	result, err = tool.Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{
		"summary": "## Current Objective\nsmall history",
	}})
	if err != nil || result.IsError {
		t.Fatalf("small-history skip must look successful: result=%#v err=%v", result, err)
	}
	if got := small.EnsureContextState().DrainPendingRefocus(); got != "" {
		t.Fatalf("small-history refocus stored pending summary %q", got)
	}
}

func TestRefocusToolValidatesSummaryByUnicodeCharacters(t *testing.T) {
	hook := NewRefocusHook()
	tool := refocusToolForTest(t, hook, newRefocusSession("refocus-validation", refocusMinHistoryTokens+1))

	for _, summary := range []string{"", "   ", strings.Repeat("界", refocusSummaryMaxChars+1)} {
		result, err := tool.Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{"summary": summary}})
		if err != nil || !result.IsError {
			t.Fatalf("summary %q: result=%#v err=%v, want tool error", summary[:min(len(summary), 10)], result, err)
		}
	}

	result, err := tool.Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{
		"summary": strings.Repeat("界", refocusSummaryMaxChars),
	}})
	if err != nil || result.IsError {
		t.Fatalf("exact rune limit should succeed: result=%#v err=%v", result, err)
	}
}

func TestApplyRefocusCompactPreservesMemoryAndCurrentTail(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	syncAt := base.Add(30 * time.Minute)
	sess := session.New("refocus-apply", nil, session.WithHistory(
		types.Message{Role: types.RoleUser, Content: "discarded discussion", Time: base},
		types.Message{Role: types.RoleAssistant, Content: "discarded answer", Time: base.Add(time.Minute)},
		types.Message{Role: types.RoleAssistant, Time: syncAt.Add(time.Minute), ToolCalls: []types.ToolCall{{ID: "call-r1", Name: refocusToolName}}},
		types.Message{Role: types.RoleTool, Time: syncAt.Add(2 * time.Minute), ToolResult: &types.ToolResult{CallID: "call-r1", Content: refocusAppliedMessage}},
	))
	st := sess.EnsureContextState()
	st.SessionMemory = []types.Message{{Role: types.RoleAgent, Content: "<session_memory>migration state</session_memory>"}}
	st.LastSyncedAt = syncAt
	st.MicroCompactPrefix = []types.Message{{Role: types.RoleAgent, Content: "frozen"}}
	st.MicroCompactSourceMessages = 2
	st.TokenCheckpoint = session.TokenCheckpoint{Index: 4, PromptTokens: 40_000}
	st.StorePendingRefocus("## Current Objective\nfinish migration")

	mgr := New(nil, Config{ContextWindow: 1_000_000})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel() error = %v", err)
	}

	got := sess.GetHistory()
	if len(got) != 4 || !strings.Contains(got[0].Content, "<session_memory>") || !strings.Contains(got[1].Content, "<refocus_summary>") {
		t.Fatalf("unexpected compacted history: %#v", got)
	}
	if got[2].ToolCalls[0].Name != refocusToolName || got[3].ToolResult == nil || got[3].ToolResult.CallID != "call-r1" {
		t.Fatalf("current tool call/result were not preserved: %#v", got[2:])
	}
	st = sess.EnsureContextState()
	if st.MicroCompactSourceMessages != 0 || len(st.MicroCompactPrefix) != 0 || st.TokenCheckpoint.PromptTokens != 0 {
		t.Fatalf("compaction state was not reset: %#v", st)
	}
}

func TestRefocusProjectionCannotRestoreDiscardedHistory(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	syncAt := base.Add(30 * time.Minute)
	sess := session.New("refocus-no-restore", nil, session.WithHistory(
		types.Message{Role: types.RoleUser, Content: "DISCARDED_MARKER " + strings.Repeat("old ", 10_000), Time: base},
		types.Message{Role: types.RoleUser, Content: strings.Repeat("current ", 2_000), Time: syncAt.Add(time.Minute)},
	))
	st := sess.EnsureContextState()
	st.LastSyncedAt = syncAt
	st.StorePendingRefocus("## Current Objective\nkeep only current work")

	mgr := New(nil, Config{ContextWindow: 1_000, SoftThresholdRatio: 0.20, HardThresholdRatio: 0.30})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel() error = %v", err)
	}
	for _, history := range [][]types.Message{sess.GetHistory(), req.History()} {
		for _, message := range history {
			if strings.Contains(message.Content, "DISCARDED_MARKER") {
				t.Fatalf("discarded history was restored: %#v", history)
			}
		}
	}
}

func TestApplyRefocusCompactPublishesPersistenceFailure(t *testing.T) {
	persistErr := errors.New("storage unavailable")
	sess := session.New("refocus-persist-fail", nil,
		session.WithMessageWriter(failingRefocusWriter{err: persistErr}),
		session.WithHistory(types.Message{Role: types.RoleUser, Content: "history"}),
	)
	eventCh, unsubscribe := sess.SubscribeEvents()
	defer unsubscribe()

	mgr := New(nil, Config{ContextWindow: 1_000_000})
	mgr.applyRefocusCompact(context.Background(), sess, "## Current Objective\ncontinue")
	events := drainEvents(eventCh)
	fail := findContextCompactEvent(events, types.EventCompactFinish, "refocus")
	if fail == nil || fail.Data["status"] != "failed" || fail.Data["trigger"] != "refocus" || fail.Data["error"] != persistErr.Error() {
		t.Fatalf("unexpected refocus persistence failure: %#v", fail)
	}
}
