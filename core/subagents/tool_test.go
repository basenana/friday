package subagents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
)

// recordingSpan captures SetAttributes calls for inspection.
type recordingSpan struct {
	tracing.Span
	attrs []tracing.Attribute
}

func (s *recordingSpan) SetAttributes(attrs ...tracing.Attribute) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *recordingSpan) End()                                     {}
func (s *recordingSpan) SetStatus(_ tracing.StatusCode, _ string) {}

func (s *recordingSpan) attrValue(key string) (string, bool) {
	for _, a := range s.attrs {
		if a.Key == key {
			v, ok := a.Value.(string)
			return v, ok
		}
	}
	return "", false
}

func TestRunTaskContractUsesBatchTasksAndAgentEnum(t *testing.T) {
	hook := &Subagents{
		option:                 Option{ExpertAgents: []ExpertAgent{{Name: "analyst"}, {Name: "writer"}}},
		runTaskToolDescription: "Delegate to an expert.",
		parallel:               make(chan struct{}, 2),
	}
	tool := hook.buildRunTaskTool(session.New("session", nil))
	for _, legacy := range []string{"task_describe", "task", "agent_name"} {
		if _, exists := tool.InputSchema.Properties[legacy]; exists {
			t.Fatalf("legacy field %q exposed", legacy)
		}
	}
	tasks, exists := tool.InputSchema.Properties["tasks"].(map[string]any)
	if !exists {
		t.Fatal("tasks field missing")
	}
	items := tasks["items"].(map[string]any)
	properties := items["properties"].(map[string]any)
	enum := properties["agent_name"].(map[string]any)["enum"].([]string)
	if len(enum) != 2 || enum[0] != "analyst" || enum[1] != "writer" {
		t.Fatalf("agent enum = %v", enum)
	}
	if _, limited := tasks["maxItems"]; limited {
		t.Fatal("task count must not be limited by the schema")
	}
	if errors := tool.ValidateDefinition(2); len(errors) != 0 {
		t.Fatalf("batch definition errors: %v", errors)
	}
}

func TestBatchSchemasDoNotLimitQueuedTaskCount(t *testing.T) {
	const taskCount = 25
	hook := &Subagents{
		option: Option{
			SelfAgent:    &ExpertAgent{Name: "explore", Agent: &fakeAgent{response: "done"}},
			ExpertAgents: []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: "done"}}},
		},
		exploreToolDescription: "Explore tasks concurrently.",
		runTaskToolDescription: "Delegate tasks concurrently.",
		parallel:               make(chan struct{}, 2),
	}
	exploreArgs := map[string]any{"tasks": make([]any, 0, taskCount)}
	runTaskArgs := map[string]any{"tasks": make([]any, 0, taskCount)}
	for i := range taskCount {
		exploreArgs["tasks"] = append(exploreArgs["tasks"].([]any), fmt.Sprintf("investigate-%d", i))
		runTaskArgs["tasks"] = append(runTaskArgs["tasks"].([]any), map[string]any{
			"agent_name": "worker",
			"task":       fmt.Sprintf("work-%d", i),
		})
	}
	if issue := hook.buildExploreTool(session.New("explore", nil)).ValidateArguments(exploreArgs); issue != "" {
		t.Fatalf("explore rejected queued tasks above parallelism: %s", issue)
	}
	if issue := hook.buildRunTaskTool(session.New("expert", nil)).ValidateArguments(runTaskArgs); issue != "" {
		t.Fatalf("run_task rejected queued tasks above parallelism: %s", issue)
	}
}

func TestNewHookConfiguresSharedActiveTaskLimit(t *testing.T) {
	defaultHook := NewHook(nil, Option{})
	if got := cap(defaultHook.parallel); got != defaultMaxParallelSubagents {
		t.Fatalf("default parallel limit = %d, want %d", got, defaultMaxParallelSubagents)
	}
	customHook := NewHook(nil, Option{MaxParallelSubagents: 7})
	if got := cap(customHook.parallel); got != 7 {
		t.Fatalf("custom parallel limit = %d, want 7", got)
	}
}

func TestPromptsRequireImmediateBatchedParallelism(t *testing.T) {
	for name, prompt := range map[string]string{
		"explore system":      EXPLORE_SYSTEM_PROMPT,
		"explore description": EXPLORE_DESCRIPTION_PROMPT,
		"expert system":       EXPERT_SYSTEM_PROMPT,
		"expert description":  EXPERT_DESCRIPTION_PROMPT,
	} {
		lower := strings.ToLower(prompt)
		for _, phrase := range []string{"tasks array", "independent"} {
			if !strings.Contains(lower, phrase) {
				t.Fatalf("%s prompt does not clearly describe %q", name, phrase)
			}
		}
		if !strings.Contains(lower, "parallel") && !strings.Contains(lower, "concurr") {
			t.Fatalf("%s prompt does not clearly require concurrent execution", name)
		}
	}
	if !strings.Contains(strings.ToLower(EXPLORE_DESCRIPTION_PROMPT), "task count itself is not limited") ||
		!strings.Contains(strings.ToLower(EXPERT_DESCRIPTION_PROMPT), "task count itself is not limited") {
		t.Fatal("tool descriptions must distinguish queued task count from active concurrency")
	}
}

func TestRunBatchSaturatesLimitQueuesWorkAndPreservesOrder(t *testing.T) {
	const (
		taskCount = 11
		limit     = 3
	)
	tasks := make([]batchTask, taskCount)
	for i := range tasks {
		tasks[i] = batchTask{Agent: "worker", Task: fmt.Sprintf("task-%02d", i)}
	}
	parallel := make(chan struct{}, limit)
	started := make(chan struct{}, taskCount)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32

	done := make(chan []batchTaskResult, 1)
	go func() {
		done <- runBatch(context.Background(), tasks, parallel, func(_ context.Context, _ int, task batchTask) batchTaskResult {
			current := active.Add(1)
			for {
				previous := peak.Load()
				if current <= previous || peak.CompareAndSwap(previous, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			return successfulBatchTask(task, "result for "+task.Task)
		})
	}()

	for range limit {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("batch did not fill all available parallel slots")
		}
	}
	select {
	case <-started:
		t.Fatal("batch started work above the active parallel limit")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)

	var results []batchTaskResult
	select {
	case results = <-done:
	case <-time.After(time.Second):
		t.Fatal("queued batch did not finish")
	}
	if got := int(peak.Load()); got != limit {
		t.Fatalf("peak concurrency = %d, want %d", got, limit)
	}
	if len(results) != taskCount {
		t.Fatalf("result count = %d, want %d", len(results), taskCount)
	}
	for i, result := range results {
		if result.Task != tasks[i].Task || result.Status != "succeeded" {
			t.Fatalf("result[%d] = %#v, want task %q succeeded", i, result, tasks[i].Task)
		}
	}
}

func TestRunBatchSharesActiveLimitAcrossConcurrentBatches(t *testing.T) {
	parallel := make(chan struct{}, 2)
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	var active atomic.Int32
	var peak atomic.Int32
	execute := func(_ context.Context, _ int, task batchTask) batchTaskResult {
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return successfulBatchTask(task, "done")
	}

	done := make(chan struct{}, 2)
	for batchIndex := range 2 {
		batch := []batchTask{
			{Agent: "worker", Task: fmt.Sprintf("batch-%d-a", batchIndex)},
			{Agent: "worker", Task: fmt.Sprintf("batch-%d-b", batchIndex)},
		}
		go func() {
			runBatch(context.Background(), batch, parallel, execute)
			done <- struct{}{}
		}()
	}
	for range cap(parallel) {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("concurrent batches did not fill the shared limit")
		}
	}
	select {
	case <-started:
		t.Fatal("concurrent batches exceeded the shared active-task limit")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("concurrent batch did not finish")
		}
	}
	if got := peak.Load(); got != int32(cap(parallel)) {
		t.Fatalf("peak shared concurrency = %d, want %d", got, cap(parallel))
	}
}

func TestBatchToolResultKeepsSuccessesOnPartialFailure(t *testing.T) {
	results := []batchTaskResult{
		successfulBatchTask(batchTask{Agent: "reader", Task: "first"}, "first report"),
		failedBatchTask(batchTask{Agent: "writer", Task: "second"}, errors.New("write failed"), "fix permissions and retry only this task"),
		successfulBatchTask(batchTask{Agent: "reader", Task: "third"}, "third report"),
	}
	result := batchToolResult("run_task", results)
	if result.IsError {
		t.Fatal("partial success must remain usable as a successful tool result")
	}
	text := result.Content[0].(tools.TextContent).Text
	for _, want := range []string{"succeeded=\"2\"", "failed=\"1\"", "first report", "write failed", "do not repeat tasks that already succeeded"} {
		if !strings.Contains(text, want) {
			t.Fatalf("partial batch result missing %q:\n%s", want, text)
		}
	}

	allFailed := batchToolResult("run_task", []batchTaskResult{results[1]})
	if !allFailed.IsError {
		t.Fatal("an all-failed batch must be an error tool result")
	}
	failedText := allFailed.Content[0].(tools.TextContent).Text
	if !strings.Contains(failedText, "Suggestion:") || !strings.Contains(failedText, "retry") {
		t.Fatalf("all-failed batch lacks actionable retry guidance: %s", failedText)
	}
}

func TestRunTaskBatchExplainsUnknownExpertWithoutDiscardingSuccess(t *testing.T) {
	sess := newTestSession(t)
	handler := callSubagentTool(
		[]ExpertAgent{{Name: "reader", Agent: &fakeAgent{response: "## Findings\nread complete"}}},
		sess,
		nil,
	)
	result, err := handler(context.Background(), &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]any{"tasks": []any{
			map[string]any{"agent_name": "reader", "task": "read the evidence"},
			map[string]any{"agent_name": "missing", "task": "perform unsupported work"},
		}},
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("partial batch failed: result=%#v err=%v", result, err)
	}
	text := result.Content[0].(tools.TextContent).Text
	for _, want := range []string{"read complete", `subagent "missing" not found`, "available agents: reader", "Choose one of the listed available agents", "do not repeat tasks that already succeeded"} {
		if !strings.Contains(text, want) {
			t.Fatalf("unknown-expert result missing %q:\n%s", want, text)
		}
	}
}

func TestExploreBatchReleasesEveryForkedSession(t *testing.T) {
	sess := newTestSession(t)
	forker := &recordingForker{root: sess}
	self := &ExpertAgent{Name: "explore", Agent: &fakeAgent{response: "## Findings\ndone"}}
	handler := callExploreToolWithForker(self, sess, forker, nil, make(chan struct{}, 2))
	rawTasks := make([]any, 9)
	for i := range rawTasks {
		rawTasks[i] = fmt.Sprintf("inspect-%d", i)
	}
	result, err := handler(context.Background(), &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]any{"tasks": rawTasks},
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("explore batch failed: result=%#v err=%v", result, err)
	}
	forks, releases := forker.counts()
	if forks != len(rawTasks) || releases != forks {
		t.Fatalf("fork/release counts = %d/%d, want %d/%d", forks, releases, len(rawTasks), len(rawTasks))
	}
	if children := sess.ChildrenSnapshot(); len(children) != 0 {
		t.Fatalf("released sessions remain attached: %d", len(children))
	}
}

// recordingTracer injects a recordingSpan so tests can inspect attributes.
type recordingTracer struct {
	span *recordingSpan
}

func (r *recordingTracer) Start(ctx context.Context, _ string, opts ...tracing.SpanOption) (context.Context, tracing.Span) {
	r.span = &recordingSpan{Span: tracing.SpanFromContext(ctx)}
	return ctx, r.span
}

// fakeAgent implements agents.Agent for testing.
type fakeAgent struct {
	response string
	chatFunc func(context.Context, *api.Request) *api.Response
}

func (f *fakeAgent) Chat(ctx context.Context, req *api.Request) *api.Response {
	if f.chatFunc != nil {
		return f.chatFunc(ctx, req)
	}
	resp := api.NewResponse()
	go func() {
		api.SendDelta(resp, types.Delta{Content: f.response})
		resp.Close()
	}()
	return resp
}

type recordingForker struct {
	root *session.Session
	mu   sync.Mutex

	forks    int
	releases int
}

func (f *recordingForker) Fork() (*session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forks++
	return f.root.Fork(), nil
}

func (f *recordingForker) Release(child *session.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	if !f.root.DetachChild(child) {
		return errors.New("child not attached")
	}
	return nil
}

func (f *recordingForker) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forks, f.releases
}

func newTestSession(t *testing.T) *session.Session {
	t.Helper()
	return session.New("test-sess", nil)
}

func TestCallSubagentToolTruncatesLongInput(t *testing.T) {
	rec := &recordingTracer{}
	tracing.SetGlobalTracer(rec)
	defer tracing.SetGlobalTracer(nil)

	sess := newTestSession(t)
	longInput := strings.Repeat("a", 3000)
	agents := []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: "done"}}}
	handler := callSubagentTool(agents, sess, nil)

	req := &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]interface{}{
			"tasks": []any{map[string]any{"agent_name": "worker", "task": longInput}},
		},
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, ok := rec.span.attrValue("subagent.input")
	if !ok {
		t.Fatal("subagent.input attribute not set")
	}
	if len(val) >= 3000 {
		t.Errorf("expected input to be truncated, got len=%d", len(val))
	}
	if !strings.HasSuffix(val, "...[truncated]") {
		t.Errorf("expected truncation marker, got: %q", val[max(0, len(val)-20):])
	}
}

func TestCallSubagentToolTruncatesLongOutput(t *testing.T) {
	rec := &recordingTracer{}
	tracing.SetGlobalTracer(rec)
	defer tracing.SetGlobalTracer(nil)

	longOutput := strings.Repeat("b", 3000)
	sess := newTestSession(t)
	agents := []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: longOutput}}}
	handler := callSubagentTool(agents, sess, nil)

	req := &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]interface{}{
			"tasks": []any{map[string]any{"agent_name": "worker", "task": "short task"}},
		},
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, ok := rec.span.attrValue("subagent.output")
	if !ok {
		t.Fatal("subagent.output attribute not set")
	}
	if len(val) >= 3000 {
		t.Errorf("expected output to be truncated, got len=%d", len(val))
	}
	if !strings.HasSuffix(val, "...[truncated]") {
		t.Errorf("expected truncation marker, got: %q", val[max(0, len(val)-20):])
	}
}

func TestCallSubagentToolShortValuesUnchanged(t *testing.T) {
	rec := &recordingTracer{}
	tracing.SetGlobalTracer(rec)
	defer tracing.SetGlobalTracer(nil)

	agents := []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: "short output"}}}
	sess := newTestSession(t)
	handler := callSubagentTool(agents, sess, nil)

	req := &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]interface{}{
			"tasks": []any{map[string]any{"agent_name": "worker", "task": "short input"}},
		},
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val, ok := rec.span.attrValue("subagent.input"); ok {
		if strings.Contains(val, "[truncated]") {
			t.Errorf("short input should not be truncated, got: %q", val)
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func TestCallSubagentTool_AntiNestingGuard(t *testing.T) {
	sess := newTestSession(t)
	agents := []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: "should-not-happen"}}}
	handler := callSubagentTool(agents, sess, nil)

	// Simulate a forked sub-session request by setting a different SessionID.
	req := &tools.Request{
		SessionID: "forked-" + sess.Root.ID,
		Arguments: map[string]interface{}{
			"tasks": []any{map[string]any{"agent_name": "worker", "task": "nested call"}},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error tool result from anti-nesting guard, got %#v", result)
	}
}

func TestCallExploreTool_AntiNestingGuard(t *testing.T) {
	sess := newTestSession(t)
	self := &ExpertAgent{Name: "explore", Agent: &fakeAgent{response: "should-not-happen"}}
	handler := callExploreTool(self, sess, nil)

	req := &tools.Request{
		SessionID: "forked-" + sess.Root.ID,
		Arguments: map[string]interface{}{
			"tasks": []any{"nested explore"},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error tool result from anti-nesting guard, got %#v", result)
	}
}
