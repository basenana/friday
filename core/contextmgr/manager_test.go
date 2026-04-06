package contextmgr

import (
	stdctx "context"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

func TestBeforeModelProjectsCaseFileAndPrunesOldToolResults(t *testing.T) {
	largeToolResult := strings.Repeat("tool output ", 200)
	sess := session.New("sess-1", nil, session.WithHistory(
		types.Message{Role: types.RoleUser, Content: "Investigate core/session/compact.go and summarize the changes."},
		types.Message{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`}}},
		types.Message{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: largeToolResult}},
		types.Message{Role: types.RoleAssistant, Content: "I found the relevant compaction code and I'm preparing the summary."},
	))

	mgr := New(nil, Config{
		ContextWindow:        800,
		SoftThresholdRatio:   0.20,
		HardThresholdRatio:   0.30,
		PreserveTailMessages: 2,
		MaxToolResultChars:   80,
	})

	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	projected := req.History()
	if len(projected) < 2 {
		t.Fatalf("expected projected history with case file and tail, got %d messages", len(projected))
	}
	if projected[0].Role != types.RoleAgent || !strings.Contains(projected[0].Content, "<case_file>") {
		t.Fatalf("expected first projected message to be a case file, got %#v", projected[0])
	}
	if _, ok := session.ParseCaseFileMessage(projected[0].Content); !ok {
		t.Fatalf("expected first projected message to contain a parseable JSON case file, got %q", projected[0].Content)
	}
	if strings.Contains(projected[0].Content, largeToolResult) {
		t.Fatalf("expected case file projection instead of raw tool result")
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
		ContextWindow:        8000,
		SoftThresholdRatio:   0.90,
		HardThresholdRatio:   0.95,
		PreserveTailMessages: 1,
		MaxToolResultChars:   40,
	})

	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	projected := req.History()
	if len(projected) < 3 {
		t.Fatalf("expected full projected history, got %d messages", len(projected))
	}
	if projected[0].Role == types.RoleAgent && strings.Contains(projected[0].Content, "<case_file>") {
		t.Fatalf("expected no projected case file before first durable compact, got %#v", projected[0])
	}
	if projected[1].ToolResult == nil || projected[1].ToolResult.Content != toolResult {
		t.Fatalf("expected old tool result to remain unpruned below soft threshold, got %#v", projected[1].ToolResult)
	}
	if containsTaggedMessage(projected, "<tool_observations>") {
		t.Fatalf("expected tool observations to stay out of normal full-history projection, got %#v", projected)
	}
}

func TestBeforeModelDoesNotOverwriteDurableCaseFileWithHeuristic(t *testing.T) {
	sess := session.New("sess-heuristic-guard", nil, session.WithHistory(
		types.Message{Role: types.RoleUser, Content: "A new user request that should not overwrite the durable objective."},
		types.Message{Role: types.RoleAssistant, Content: "Some transient assistant status."},
	))
	st := sess.EnsureContextState()
	st.CaseFile = session.CaseFile{
		TaskObjective: "Durable objective from LLM compaction",
		CurrentStatus: "Durable status from LLM compaction",
		PendingWork:   []string{"durable pending item"},
	}

	mgr := New(nil, Config{ContextWindow: 8000})
	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	if st.CaseFile.TaskObjective != "Durable objective from LLM compaction" {
		t.Fatalf("expected durable task objective to remain unchanged, got %q", st.CaseFile.TaskObjective)
	}
	if st.CaseFile.CurrentStatus != "Durable status from LLM compaction" {
		t.Fatalf("expected durable current status to remain unchanged, got %q", st.CaseFile.CurrentStatus)
	}
	if len(st.CaseFile.PendingWork) != 1 || st.CaseFile.PendingWork[0] != "durable pending item" {
		t.Fatalf("expected durable pending work to remain unchanged, got %#v", st.CaseFile.PendingWork)
	}
	projected := req.History()
	if len(projected) == 0 {
		t.Fatalf("expected projected history to be populated")
	}
	cf, ok := session.ParseCaseFileMessage(projected[0].Content)
	if !ok {
		t.Fatalf("expected projected history to start with durable case file, got %q", projected[0].Content)
	}
	if cf.TaskObjective != "Durable objective from LLM compaction" {
		t.Fatalf("expected projected durable task objective, got %q", cf.TaskObjective)
	}
}

func TestAfterToolPreservesExecutionToToolAssociation(t *testing.T) {
	mgr := New(nil, Config{})
	sess := session.New("sess-2", nil)
	sess.EnsureContextState().CaseFile = session.CaseFile{
		TaskObjective: "Durable objective",
		CurrentStatus: "Durable status",
	}
	now := time.Now()

	payload := session.ToolPayload{
		Executions: []session.ToolExecution{
			{
				Call: providers.ToolCall{Name: "read_file", ID: "call-1"},
				Messages: []types.Message{
					{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`}}, Time: now},
					{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-1", Content: `{"content":[{"type":"text","text":"core/session/compact.go"}]}`}, Time: now},
				},
			},
			{
				Call: providers.ToolCall{Name: "list_dir", ID: "call-2"},
				Messages: []types.Message{
					{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{ID: "call-2", Name: "list_dir", Arguments: `{"path":"core/session"}`}}, Time: now},
					{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "call-2", Content: `{"content":[{"type":"text","text":"compact.go\nsession.go"}]}`}, Time: now},
				},
			},
		},
	}

	if err := mgr.AfterTool(stdctx.Background(), sess, payload); err != nil {
		t.Fatalf("AfterTool failed: %v", err)
	}

	st := sess.EnsureContextState()
	if len(st.ToolObservations) != 2 {
		t.Fatalf("expected 2 tool observations, got %d", len(st.ToolObservations))
	}
	if st.ToolObservations[0].ToolName != "read_file" || st.ToolObservations[1].ToolName != "list_dir" {
		t.Fatalf("unexpected tool observation ordering or association: %#v", st.ToolObservations)
	}
	if st.CaseFile.TaskObjective != "Durable objective" || st.CaseFile.CurrentStatus != "Durable status" {
		t.Fatalf("expected AfterTool not to mutate durable case file, got %#v", st.CaseFile)
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
	sess.EnsureContextState().ToolObservations = []session.ToolObservation{
		{
			ToolName: "read_file",
			Summary:  "Recovered the large tool output from core/session/compact.go.",
			Success:  true,
			Files:    []string{"core/session/compact.go"},
		},
	}

	mgr := New(nil, Config{
		ContextWindow:        1000,
		SoftThresholdRatio:   0.10,
		HardThresholdRatio:   0.15,
		PreserveTailMessages: 1,
	})

	req := providers.NewRequest("", sess.GetHistory()...)
	if err := mgr.BeforeModel(stdctx.Background(), sess, req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}

	if writer.replaceCalls != 1 {
		t.Fatalf("expected ReplaceHistory to be called once, got %d", writer.replaceCalls)
	}
	if len(sess.GetHistory()) < 2 || !strings.Contains(sess.GetHistory()[1].Content, "<case_file>") {
		t.Fatalf("expected durable case file in history after hard compact, got %#v", sess.GetHistory())
	}
	if _, ok := session.ParseCaseFileMessage(sess.GetHistory()[1].Content); !ok {
		t.Fatalf("expected durable case file in history to be JSON parseable, got %q", sess.GetHistory()[1].Content)
	}
	if containsTaggedMessage(sess.GetHistory(), "<tool_observations>") {
		t.Fatalf("expected tool observations to stay out of persisted session history, got %#v", sess.GetHistory())
	}
	if containsTaggedMessage(req.History(), "<tool_observations>") {
		t.Fatalf("expected tool observations not to be projected after hard compact, got %#v", req.History())
	}
}

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

func containsTaggedMessage(history []types.Message, tag string) bool {
	for _, msg := range history {
		if strings.Contains(msg.Content, tag) {
			return true
		}
	}
	return false
}
