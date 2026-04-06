package session

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
	"github.com/invopop/jsonschema"
)

func TestHeuristicCaseFileTracksPendingWorkAndFiles(t *testing.T) {
	now := time.Now()
	history := []types.Message{
		{Role: types.RoleUser, Content: "Please refactor core/session/compact.go and keep backward compatibility.", Time: now},
		{Role: types.RoleAgent, Content: "<current_todo_list>\ndescribe=refactor compaction status=in_progress\ndescribe=write tests status=pending\n</current_todo_list>", Time: now},
		{Role: types.RoleAssistant, ToolCalls: []types.ToolCall{{Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`}}, Time: now},
		{Role: types.RoleTool, ToolResult: &types.ToolResult{CallID: "1", Content: `{"content":[{"type":"text","text":"updated core/session/compact.go"}]}`}, Time: now},
	}

	cf := HeuristicCaseFile(history)

	if cf.TaskObjective == "" {
		t.Fatalf("expected task objective to be populated")
	}
	if len(cf.PendingWork) != 2 {
		t.Fatalf("expected 2 pending items, got %d", len(cf.PendingWork))
	}
	if len(cf.RecentFiles) == 0 || cf.RecentFiles[0] != "core/session/compact.go" {
		t.Fatalf("expected recent files to include compact.go, got %#v", cf.RecentFiles)
	}
	formatted := cf.String()
	if !strings.Contains(formatted, "<case_file>") || !strings.Contains(formatted, `"task_objective"`) {
		t.Fatalf("expected JSON case file wrapper, got %q", formatted)
	}
	parsed, ok := ParseCaseFileMessage(formatted)
	if !ok {
		t.Fatalf("expected formatted case file to be parseable")
	}
	if parsed.TaskObjective != cf.TaskObjective {
		t.Fatalf("expected parsed task objective %q, got %q", cf.TaskObjective, parsed.TaskObjective)
	}
}

func TestParseCaseFileMessageRejectsLegacyTextFormat(t *testing.T) {
	legacy := `<case_file>
## Task Objective
legacy format
</case_file>`

	if _, ok := ParseCaseFileMessage(legacy); ok {
		t.Fatalf("expected legacy text case file format to be rejected")
	}
}

func TestSessionRestoresCaseFileFromHistory(t *testing.T) {
	cf := CaseFile{
		TaskObjective: "Keep the compacted session durable across reloads.",
		PendingWork:   []string{"persist case file"},
		RecentFiles:   []string{"core/session/compact.go"},
	}
	history := BuildCompactedHistory([]types.Message{
		{Role: types.RoleUser, Content: "Please improve compaction."},
		{Role: types.RoleAssistant, Content: "Working on it."},
	}, cf, 1)

	sess := New("restored", nil, WithHistory(history...))
	if sess.Context == nil {
		t.Fatalf("expected context to be restored")
	}
	if sess.Context.CaseFile.TaskObjective != cf.TaskObjective {
		t.Fatalf("expected restored task objective %q, got %q", cf.TaskObjective, sess.Context.CaseFile.TaskObjective)
	}
	if len(sess.Context.CaseFile.PendingWork) != 1 || sess.Context.CaseFile.PendingWork[0] != "persist case file" {
		t.Fatalf("expected restored pending work, got %#v", sess.Context.CaseFile.PendingWork)
	}
}

func TestCaseFileJSONSchemaHasRequiredFieldsAndDescriptions(t *testing.T) {
	schema := (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&CaseFile{})
	if schema.Properties == nil {
		t.Fatalf("expected case file schema properties")
	}

	required := []string{
		"task_objective",
		"user_constraints",
		"architecture_decisions",
		"current_status",
		"pending_work",
		"recent_requests",
		"recent_files",
		"important_commands_or_tools",
		"known_risks",
		"timeline_highlights",
	}
	for _, field := range required {
		if !slices.Contains(schema.Required, field) {
			t.Fatalf("expected %q to be required, got %#v", field, schema.Required)
		}
	}

	taskObjective, ok := schema.Properties.Get("task_objective")
	if !ok {
		t.Fatalf("expected task_objective property in schema")
	}
	if !strings.Contains(taskObjective.Description, "Primary user objective") {
		t.Fatalf("expected task_objective description, got %q", taskObjective.Description)
	}

	recentFiles, ok := schema.Properties.Get("recent_files")
	if !ok {
		t.Fatalf("expected recent_files property in schema")
	}
	if recentFiles.MaxItems == nil || *recentFiles.MaxItems != 5 {
		t.Fatalf("expected recent_files maxItems=5, got %#v", recentFiles.MaxItems)
	}
}

func TestSummarizeToCaseFileUsesStructuredPredictFirst(t *testing.T) {
	llm := &fakeCaseFileClient{
		structuredCaseFile: CaseFile{
			TaskObjective: "Structured summary objective",
			PendingWork:   []string{"structured follow-up"},
		},
		completionChunks: []string{`{"task_objective":"stream fallback should not run"}`},
	}

	summary, err := SummarizeToCaseFile(context.Background(), llm, CaseFile{}, []types.Message{
		{Role: types.RoleUser, Content: "Summarize this session."},
	})
	if err != nil {
		t.Fatalf("SummarizeToCaseFile() error = %v", err)
	}
	if llm.structuredCalls != 1 {
		t.Fatalf("expected StructuredPredict to be called once, got %d", llm.structuredCalls)
	}
	if llm.completionCalls != 0 {
		t.Fatalf("expected Completion fallback not to run, got %d calls", llm.completionCalls)
	}
	if summary.TaskObjective != "Structured summary objective" {
		t.Fatalf("expected structured summary to win, got %q", summary.TaskObjective)
	}
	if len(summary.PendingWork) != 1 || summary.PendingWork[0] != "structured follow-up" {
		t.Fatalf("expected structured pending work, got %#v", summary.PendingWork)
	}
}

func TestSummarizeToCaseFileFallsBackToStreamingCompletion(t *testing.T) {
	llm := &fakeCaseFileClient{
		structuredErr: errors.New("structured predict unavailable"),
		completionChunks: []string{
			`{"task_objective":"streamed summary",`,
			`"pending_work":["finish tests"]}`,
		},
	}

	summary, err := SummarizeToCaseFile(context.Background(), llm, CaseFile{}, []types.Message{
		{Role: types.RoleUser, Content: "Summarize this session."},
	})
	if err != nil {
		t.Fatalf("SummarizeToCaseFile() error = %v", err)
	}
	if llm.structuredCalls != 1 {
		t.Fatalf("expected StructuredPredict to be attempted once, got %d", llm.structuredCalls)
	}
	if llm.completionCalls != 1 {
		t.Fatalf("expected Completion fallback to run once, got %d", llm.completionCalls)
	}
	if summary.TaskObjective != "streamed summary" {
		t.Fatalf("expected streamed summary objective, got %q", summary.TaskObjective)
	}
	if len(summary.PendingWork) != 1 || summary.PendingWork[0] != "finish tests" {
		t.Fatalf("expected streamed pending work, got %#v", summary.PendingWork)
	}
}

func TestSummarizeToCaseFileFallsBackToHeuristicWhenLLMOutputFails(t *testing.T) {
	llm := &fakeCaseFileClient{
		structuredErr:    errors.New("structured predict unavailable"),
		completionChunks: []string{"not valid json"},
	}

	history := []types.Message{
		{Role: types.RoleUser, Content: "Please keep core/session/casefile.go stable."},
		{Role: types.RoleAssistant, Content: "I will preserve the existing behavior."},
	}

	summary, err := SummarizeToCaseFile(context.Background(), llm, CaseFile{}, history)
	if err != nil {
		t.Fatalf("SummarizeToCaseFile() error = %v", err)
	}
	if summary.TaskObjective != "Please keep core/session/casefile.go stable." {
		t.Fatalf("expected heuristic task objective fallback, got %q", summary.TaskObjective)
	}
	if !strings.Contains(summary.CurrentStatus, "preserve the existing behavior") {
		t.Fatalf("expected heuristic current status fallback, got %q", summary.CurrentStatus)
	}
}

func TestSummarizeToCaseFileKeepsExistingWhenLLMFails(t *testing.T) {
	llm := &fakeCaseFileClient{
		structuredErr: errors.New("structured predict unavailable"),
		completionErr: errors.New("streaming completion failed"),
	}

	existing := CaseFile{
		TaskObjective: "durable objective",
		CurrentStatus: "durable status",
		PendingWork:   []string{"durable pending item"},
	}
	history := []types.Message{
		{Role: types.RoleUser, Content: "new ambiguous request from heuristic"},
		{Role: types.RoleAssistant, Content: "new ambiguous status from heuristic"},
	}

	summary, err := SummarizeToCaseFile(context.Background(), llm, existing, history)
	if err != nil {
		t.Fatalf("SummarizeToCaseFile() error = %v", err)
	}
	if summary.TaskObjective != existing.TaskObjective {
		t.Fatalf("expected existing task objective %q, got %q", existing.TaskObjective, summary.TaskObjective)
	}
	if summary.CurrentStatus != existing.CurrentStatus {
		t.Fatalf("expected existing current status %q, got %q", existing.CurrentStatus, summary.CurrentStatus)
	}
	if len(summary.PendingWork) != 1 || summary.PendingWork[0] != "durable pending item" {
		t.Fatalf("expected existing pending work to remain intact, got %#v", summary.PendingWork)
	}
}

type fakeCaseFileClient struct {
	structuredCaseFile CaseFile
	structuredErr      error
	completionChunks   []string
	completionErr      error
	structuredCalls    int
	completionCalls    int
}

func (f *fakeCaseFileClient) Completion(ctx context.Context, request providers.Request) providers.Response {
	f.completionCalls++
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		for _, chunk := range f.completionChunks {
			select {
			case <-ctx.Done():
				resp.Err <- ctx.Err()
				return
			case resp.Stream <- providers.Delta{Content: chunk}:
			}
		}
		if f.completionErr != nil {
			resp.Err <- f.completionErr
		}
	}()
	return resp
}

func (f *fakeCaseFileClient) CompletionNonStreaming(ctx context.Context, request providers.Request) (string, error) {
	return "", errors.New("unexpected CompletionNonStreaming call")
}

func (f *fakeCaseFileClient) StructuredPredict(ctx context.Context, request providers.Request, model any) error {
	f.structuredCalls++
	if f.structuredErr != nil {
		return f.structuredErr
	}

	cf, ok := model.(*CaseFile)
	if !ok {
		return errors.New("model is not *CaseFile")
	}
	*cf = f.structuredCaseFile
	return nil
}
