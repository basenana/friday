package contextmgr

import (
	stdctx "context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

func TestBeforeModelMicroCompactPrunesOldToolResults(t *testing.T) {
	largeToolResult := strings.Repeat("tool output ", 200)
	// Three user messages => three conversation groups.
	// The first group has the large tool result and should be micro-compacted
	// while the last two groups remain untouched.
	sess := session.New("sess-1", nil, session.WithHistory(
		types.Message{Role: types.RoleUser, Content: "Investigate core/session/compact.go."},
		types.Message{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`}}},
		types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: largeToolResult}},
		types.Message{Role: types.RoleAssistant, Content: "Found the file."},
		types.Message{Role: types.RoleUser, Content: "Now summarize the changes."},
		types.Message{Role: types.RoleAssistant, Content: "I'm preparing the summary."},
		types.Message{Role: types.RoleUser, Content: "Give me the final answer."},
		types.Message{Role: types.RoleAssistant, Content: "I'm drafting the final response."},
	))

	mgr := New(nil, Config{
		ContextWindow:      1200,
		SoftThresholdRatio: 0.40,
		HardThresholdRatio: 0.60,
		MaxToolResultChars: 80,
	})

	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	projected := req.History()
	if len(projected) < 4 {
		t.Fatalf("expected projected history with pruned tool results, got %d messages", len(projected))
	}
	// The large tool result should have been pruned in microcompact mode
	for _, msg := range projected {
		if msg.Role == types.RoleTool && msg.ToolResult != nil {
			if strings.Contains(msg.ToolResult.Content, largeToolResult) {
				t.Fatalf("expected large tool result to be pruned, but found it in projection")
			}
		}
	}
}

func TestBeforeModelKeepsFullHistoryBelowSoftThreshold(t *testing.T) {
	toolResult := strings.Repeat("tool output ", 20)
	sess := session.New("sess-full-history", nil, session.WithHistory(
		types.Message{Role: types.RoleUser, Content: "Inspect core/contextmgr/manager.go."},
		types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: toolResult}},
		types.Message{Role: types.RoleAssistant, Content: "I inspected the file."},
	))

	mgr := New(nil, Config{
		ContextWindow:      8000,
		SoftThresholdRatio: 0.90,
		HardThresholdRatio: 0.95,
		MaxToolResultChars: 40,
	})

	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	projected := req.History()
	if len(projected) < 3 {
		t.Fatalf("expected full projected history, got %d messages", len(projected))
	}
	if projected[1].ToolResult == nil || projected[1].ToolResult.Content != toolResult {
		t.Fatalf("expected old tool result to remain unpruned below soft threshold, got %#v", projected[1].ToolResult)
	}
}

func TestBeforeModelHardCompactRewritesHistory(t *testing.T) {
	writer := &mockMessageWriter{}
	sess := session.New("sess-3", nil,
		session.WithMessageWriter(writer),
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: strings.Repeat("need context ", 80)},
			types.Message{Role: types.RoleAssistant, Content: strings.Repeat("assistant output ", 80)},
			types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: strings.Repeat("tool output ", 120)}},
		),
	)

	llm := &simpleCompletionClient{fixedResponse: "Summary of the conversation so far."}
	mgr := New(llm, Config{
		ContextWindow:      1000,
		SoftThresholdRatio: 0.10,
		HardThresholdRatio: 0.15,
	})

	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	if writer.replaceCalls != 1 {
		t.Fatalf("expected ReplaceHistory to be called once, got %d", writer.replaceCalls)
	}
	// After compact, the history should contain a compact summary message
	if len(sess.GetHistory()) < 2 {
		t.Fatalf("expected at least 2 messages in compacted history, got %d", len(sess.GetHistory()))
	}
	// The compact summary should use the summaryPrefix and lead the history
	if !strings.Contains(sess.GetHistory()[0].Content, "Several lengthy dialogues") {
		t.Fatalf("expected compact summary message at the start of history after hard compact, got %q", sess.GetHistory()[0].Content)
	}
}

func TestBeforeModelUsesSessionMemoryBoundaryAtHardThreshold(t *testing.T) {
	largeToolResult := strings.Repeat("tool output ", 100)

	past := time.Now().Add(-10 * time.Minute)
	recent := time.Now()

	store := &mockSessionMemoryStore{}
	sess := session.New("sess-sm-boundary", nil,
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: "Investigate core/session/compact.go and summarize the changes.", Time: past},
			types.Message{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`}}, Time: past},
			types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: largeToolResult}, Time: past},
			types.Message{Role: types.RoleAssistant, Content: "I found the relevant compaction code.", Time: recent},
		),
	)

	// Pre-set session memory and boundary timestamp: synced up to the past,
	// so only the last message (recent) is the tail.
	syncBoundary := past.Add(5 * time.Minute) // between past and recent
	st := sess.EnsureContextState()
	st.LastSyncedAt = syncBoundary
	st.SessionMemory = []types.Message{
		{Role: types.RoleAgent, Content: "<session_memory>\ntask_objective: investigate compact.go\ncurrent_status: found relevant code\n</session_memory>"},
	}

	mgr := New(nil, Config{
		ContextWindow:      800,
		SoftThresholdRatio: 0.20, // soft = 160
		HardThresholdRatio: 0.15, // hard = 120 (intentionally below soft to force session memory path)
		SessionMemoryStore: store,
	})

	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	projected := req.History()
	// Session memory boundary projection should replace pre-boundary messages
	if !containsTaggedMessage(projected, "<session_memory>") {
		t.Fatalf("expected projected history to contain session memory boundary, got %d messages", len(projected))
	}
}

func TestBeforeModelForkDoesNotReadSessionMemoryFromRootSessionFile(t *testing.T) {
	store := &mockSessionMemoryStore{
		record: &SessionMemoryRecord{
			TaskObjective: "root session objective",
			CurrentStatus: "root session status",
			LastSyncAt:    time.Now(),
		},
	}

	rootSess := session.New("root-sess", nil,
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: "Root user request."},
			types.Message{Role: types.RoleAssistant, Content: "Root assistant response."},
		),
	)

	// Create a forked session with Root pointing to rootSess
	forkedSess := session.New("forked-sess", nil,
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: "Root user request."},
			types.Message{Role: types.RoleAssistant, Content: "Root assistant response."},
		),
	)
	forkedSess.Root = rootSess

	mgr := New(nil, Config{
		ContextWindow:      8000,
		SessionMemoryStore: store,
	})

	req := providers.NewRequest("", forkedSess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), forkedSess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	st := forkedSess.EnsureContextState()
	if len(st.SessionMemory) != 0 {
		t.Fatalf("expected forked session NOT to load session memory from root session file, got %q", st.SessionMemory[0].Content)
	}
}

func TestBeforeModelForkDoesNotWriteSessionMemory(t *testing.T) {
	store := &mockSessionMemoryStore{}

	rootSess := session.New("root-sess-write", nil,
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: "Root user request."},
			types.Message{Role: types.RoleAssistant, Content: "Root assistant response."},
		),
	)

	forkedSess := session.New("forked-sess-write", nil,
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: "Root user request."},
			types.Message{Role: types.RoleAssistant, Content: "Root assistant response."},
		),
	)
	forkedSess.Root = rootSess

	mgr := New(nil, Config{
		ContextWindow:          8000,
		SessionMemoryStore:     store,
		SessionMemoryThreshold: 10, // Low threshold would trigger generation for root
	})

	req := providers.NewRequest("", forkedSess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), forkedSess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	if len(store.writes) != 0 {
		t.Fatalf("expected forked session NOT to write session memory, got %d writes", len(store.writes))
	}
}

func TestBeforeModelForkDrainsOwnPendingSessionMemory(t *testing.T) {
	rootSess := session.New("root-sess-pending", nil,
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: "Root user request."},
			types.Message{Role: types.RoleAssistant, Content: "Root assistant response."},
		),
	)

	forkedSess := session.New("forked-sess-pending", nil,
		session.WithHistory(
			types.Message{Role: types.RoleUser, Content: "Root user request."},
			types.Message{Role: types.RoleAssistant, Content: "Root assistant response."},
		),
	)
	forkedSess.Root = rootSess

	// Pre-populate the forked session's pending memory
	record := &SessionMemoryRecord{
		TaskObjective: "fork objective",
		CurrentStatus: "fork status",
		LastSyncAt:    time.Now(),
	}
	forkedSess.EnsureContextState().StorePendingMemory(record)

	mgr := New(nil, Config{
		ContextWindow:      8000,
		SessionMemoryStore: nil, // no store needed
	})

	req := providers.NewRequest("", forkedSess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), forkedSess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	st := forkedSess.EnsureContextState()
	if len(st.SessionMemory) == 0 {
		t.Fatalf("expected forked session to drain its own pending session memory")
	}
	if !strings.Contains(st.SessionMemory[0].Content, "fork objective") {
		t.Fatalf("expected session memory to contain fork objective, got %q", st.SessionMemory[0].Content)
	}
	if st.PendingMemory != nil {
		t.Fatalf("expected pending memory to be drained (nil), got %v", st.PendingMemory)
	}
}

func TestGenerateSessionMemoryRecordIncrementalOnlySendsNewHistory(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)
	t3 := t0.Add(3 * time.Minute)

	fullHistory := []types.Message{
		{Role: types.RoleUser, Content: "message 0", Time: t0},
		{Role: types.RoleAssistant, Content: "response 1", Time: t1},
		{Role: types.RoleUser, Content: "message 2", Time: t2},
		{Role: types.RoleAssistant, Content: "response 3", Time: t3},
	}

	existingRecord := &SessionMemoryRecord{
		TaskObjective: "existing objective",
		CurrentStatus: "existing status",
		LastSyncAt:    t1, // synced up to message 1
	}

	llm := &capturePromptClient{
		structuredResult: SessionMemoryRecord{
			TaskObjective: "updated objective",
			CurrentStatus: "updated status",
		},
	}

	record := generateSessionMemoryRecord(stdctx.Background(), llm, fullHistory, existingRecord, t1)
	if record == nil {
		t.Fatalf("expected non-nil record")
	}
	// Verify only incremental history was sent (messages after t1)
	if !strings.Contains(llm.capturedPrompt, "message 2") {
		t.Fatalf("expected incremental history to include message 2")
	}
	if strings.Contains(llm.capturedPrompt, "message 0") {
		t.Fatalf("expected incremental history NOT to include message 0 (already synced)")
	}
}

func TestGenerateSessionMemoryRecordNoIncrementWithFullSync(t *testing.T) {
	now := time.Now()

	fullHistory := []types.Message{
		{Role: types.RoleUser, Content: "message 0", Time: now},
		{Role: types.RoleAssistant, Content: "response 1", Time: now.Add(time.Second)},
	}

	existingRecord := &SessionMemoryRecord{
		TaskObjective: "existing objective",
		LastSyncAt:    now.Add(time.Hour), // Far in the future — nothing new
	}

	llm := &capturePromptClient{}

	record := generateSessionMemoryRecord(stdctx.Background(), llm, fullHistory, existingRecord, now.Add(time.Hour))
	if record != existingRecord {
		t.Fatalf("expected existing record to be returned when all messages are before syncAfter")
	}
	if llm.structuredCalls != 0 {
		t.Fatalf("expected no LLM calls when nothing new to sync, got %d", llm.structuredCalls)
	}
}

// Mock types

type mockMessageWriter struct {
	replaceCalls int
}

func (m *mockMessageWriter) AppendMessages(_ string, _ ...types.Message) error {
	return nil
}

func (m *mockMessageWriter) ReplaceMessages(_ string, _ ...types.Message) error {
	m.replaceCalls++
	return nil
}

type mockSessionMemoryStore struct {
	record *SessionMemoryRecord
	writes []*SessionMemoryRecord
	mu     sync.Mutex
}

func (m *mockSessionMemoryStore) WriteSessionMemory(sessionID string, record *SessionMemoryRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes = append(m.writes, record)
	return nil
}

func (m *mockSessionMemoryStore) ReadSessionMemory(sessionID string) (*SessionMemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.record, nil
}

type simpleCompletionClient struct {
	fixedResponse string
}

func (s *simpleCompletionClient) Completion(_ stdctx.Context, _ providers.Request) providers.Response {
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		resp.Stream <- providers.Delta{Content: s.fixedResponse}
	}()
	return resp
}

func (s *simpleCompletionClient) CompletionNonStreaming(_ stdctx.Context, _ providers.Request) (string, error) {
	return s.fixedResponse, nil
}

func (s *simpleCompletionClient) StructuredPredict(_ stdctx.Context, _ providers.Request, _ any) error {
	return errors.New("structured predict unavailable")
}

type failingCompactClient struct {
	completionCalls int
}

func (f *failingCompactClient) Completion(_ stdctx.Context, _ providers.Request) providers.Response {
	f.completionCalls++
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		resp.Err <- errors.New("LLM unavailable")
	}()
	return resp
}

func (f *failingCompactClient) CompletionNonStreaming(_ stdctx.Context, _ providers.Request) (string, error) {
	return "", errors.New("LLM unavailable")
}

func (f *failingCompactClient) StructuredPredict(_ stdctx.Context, _ providers.Request, _ any) error {
	return errors.New("structured predict unavailable")
}

type capturePromptClient struct {
	capturedPrompt   string
	structuredCalls  int
	structuredResult SessionMemoryRecord
}

func (c *capturePromptClient) Completion(_ stdctx.Context, req providers.Request) providers.Response {
	c.capturedPrompt = req.SystemPrompt()
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		result, _ := json.Marshal(c.structuredResult)
		resp.Stream <- providers.Delta{Content: string(result)}
	}()
	return resp
}

func (c *capturePromptClient) CompletionNonStreaming(_ stdctx.Context, req providers.Request) (string, error) {
	c.capturedPrompt = req.SystemPrompt()
	result, _ := json.Marshal(c.structuredResult)
	return string(result), nil
}

func (c *capturePromptClient) StructuredPredict(_ stdctx.Context, req providers.Request, model any) error {
	c.structuredCalls++
	c.capturedPrompt = req.SystemPrompt()
	record, ok := model.(*SessionMemoryRecord)
	if !ok {
		return errors.New("model is not *SessionMemoryRecord")
	}
	*record = c.structuredResult
	return nil
}

func containsTaggedMessage(history []types.Message, tag string) bool {
	for _, msg := range history {
		if strings.Contains(msg.Content, tag) {
			return true
		}
	}
	return false
}
