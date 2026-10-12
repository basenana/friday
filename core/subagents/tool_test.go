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

	"github.com/basenana/friday/core/agents"
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

func TestSideCheckContractUsesFocusedChecks(t *testing.T) {
	hook := &Subagents{
		option:                   Option{SideCheckAgent: &ExpertAgent{Name: "side_checker", Agent: &fakeAgent{response: "done"}}},
		sideCheckToolDescription: SIDE_CHECK_DESCRIPTION_PROMPT,
		parallel:                 make(chan struct{}, 2),
	}
	tool := hook.buildSideCheckTool(session.New("session", nil))
	if tool.Name != "run_side_checks" {
		t.Fatalf("tool name = %q", tool.Name)
	}
	checks, exists := tool.InputSchema.Properties["checks"].(map[string]any)
	if !exists {
		t.Fatal("checks field missing")
	}
	if _, limited := checks["maxItems"]; limited {
		t.Fatal("check count must not be limited by the schema")
	}
	items := checks["items"].(map[string]any)
	if description, _ := items["description"].(string); !strings.Contains(description, "read-only") {
		t.Fatalf("check item description = %q", description)
	}
	if errors := tool.ValidateDefinition(2); len(errors) != 0 {
		t.Fatalf("side-check definition errors: %v", errors)
	}
}

func TestBatchSchemasDoNotLimitQueuedTaskCount(t *testing.T) {
	const taskCount = 25
	hook := &Subagents{
		option: Option{
			SideCheckAgent: &ExpertAgent{Name: "side_checker", Agent: &fakeAgent{response: "done"}},
			ExpertAgents:   []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: "done"}}},
		},
		sideCheckToolDescription: "Run focused checks concurrently.",
		runTaskToolDescription:   "Delegate tasks concurrently.",
		parallel:                 make(chan struct{}, 2),
	}
	sideCheckArgs := map[string]any{"checks": make([]any, 0, taskCount)}
	runTaskArgs := map[string]any{"tasks": make([]any, 0, taskCount)}
	for i := range taskCount {
		sideCheckArgs["checks"] = append(sideCheckArgs["checks"].([]any), fmt.Sprintf("check-%d", i))
		runTaskArgs["tasks"] = append(runTaskArgs["tasks"].([]any), map[string]any{
			"agent_name": "worker",
			"task":       fmt.Sprintf("work-%d", i),
		})
	}
	if issue := hook.buildSideCheckTool(session.New("side-check", nil)).ValidateArguments(sideCheckArgs); issue != "" {
		t.Fatalf("run_side_checks rejected queued checks above parallelism: %s", issue)
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

func TestSideCheckPromptsDefineFocusedContextAwareChecks(t *testing.T) {
	for name, prompt := range map[string]string{
		"system":      SIDE_CHECK_SYSTEM_PROMPT,
		"description": SIDE_CHECK_DESCRIPTION_PROMPT,
	} {
		lower := strings.ToLower(prompt)
		for _, phrase := range []string{
			"read-only",
			"current conversation",
			"single check is normal",
			"cannot",
			"broad",
			"run_task",
		} {
			if !strings.Contains(lower, phrase) {
				t.Fatalf("%s prompt does not clearly describe %q", name, phrase)
			}
		}
	}
	for name, prompt := range map[string]string{
		"expert system":      EXPERT_SYSTEM_PROMPT,
		"expert description": EXPERT_DESCRIPTION_PROMPT,
	} {
		if !strings.Contains(prompt, "run_side_checks") {
			t.Fatalf("%s does not direct focused read-only checks to run_side_checks", name)
		}
	}
}

func TestSubagentTaskPromptsDefineDelegatedExecution(t *testing.T) {
	for name, tc := range map[string]struct {
		task         string
		prompt       string
		modeMarker   string
		reportMarker string
	}{
		"side check": {
			task:         "verify the parser behavior",
			prompt:       injectSideCheckReportRequest("verify the parser behavior"),
			modeMarker:   "<side_check_execution>",
			reportMarker: "- Findings",
		},
		"run_task": {
			task:         "fix the parser",
			prompt:       injectStructuredReportRequest("fix the parser"),
			modeMarker:   "<subagent_execution>",
			reportMarker: "- What Changed",
		},
	} {
		if !strings.HasPrefix(tc.prompt, tc.task+"\n\n") {
			t.Fatalf("%s task prompt does not preserve the assigned task first:\n%s", name, tc.prompt)
		}
		modeIndex := strings.Index(tc.prompt, tc.modeMarker)
		reportIndex := strings.Index(tc.prompt, tc.reportMarker)
		if modeIndex < 0 || reportIndex < 0 || modeIndex >= reportIndex {
			t.Fatalf("%s task prompt must put delegated execution guidance before the report contract:\n%s", name, tc.prompt)
		}

		lower := strings.ToLower(tc.prompt)
		phrases := []string{"do not delegate", "evidence"}
		if name == "side check" {
			phrases = append(phrases,
				"focused side check",
				"current conversation history",
				"do not call run_side_checks or run_task",
				"do not broaden the check into general research",
				"stop once the question is answered",
			)
		} else {
			phrases = append(phrases,
				"delegated subagent mode",
				"focus exclusively on the assigned task",
				"do not call run_task or run_side_checks",
				"do not stop at a plan",
			)
		}
		for _, phrase := range phrases {
			if !strings.Contains(lower, phrase) {
				t.Fatalf("%s task prompt does not clearly require %q:\n%s", name, phrase, tc.prompt)
			}
		}
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

func TestSideCheckBatchReleasesEveryForkedSession(t *testing.T) {
	sess := newTestSession(t)
	forker := &recordingForker{root: sess}
	sideChecker := &ExpertAgent{Name: "side_checker", Agent: &fakeAgent{response: "## Findings\ndone"}}
	handler := callSideCheckToolWithForker(sideChecker, sess, forker, nil, make(chan struct{}, 2))
	rawChecks := make([]any, 9)
	for i := range rawChecks {
		rawChecks[i] = fmt.Sprintf("check-%d", i)
	}
	result, err := handler(context.Background(), &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]any{"checks": rawChecks},
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("side-check batch failed: result=%#v err=%v", result, err)
	}
	forks, releases := forker.counts()
	if forks != len(rawChecks) || releases != forks {
		t.Fatalf("fork/release counts = %d/%d, want %d/%d", forks, releases, len(rawChecks), len(rawChecks))
	}
	if children := sess.ChildrenSnapshot(); len(children) != 0 {
		t.Fatalf("released sessions remain attached: %d", len(children))
	}
}

func TestSideCheckInheritsParentConversation(t *testing.T) {
	sess := newTestSession(t)
	sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "parent context"})
	forker := &recordingForker{root: sess}
	requests := make(chan *api.Request, 1)
	sideChecker := &ExpertAgent{Name: "side_checker", Agent: &fakeAgent{chatFunc: func(_ context.Context, req *api.Request) *api.Response {
		requests <- req
		resp := api.NewResponse()
		go func() {
			api.SendDelta(resp, types.Delta{Content: "## Findings\nverified"})
			resp.Close()
		}()
		return resp
	}}}
	handler := callSideCheckToolWithForker(sideChecker, sess, forker, nil, make(chan struct{}, 1))

	result, err := handler(context.Background(), &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]any{"checks": []any{"verify using the parent context"}},
	})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("side check failed: result=%#v err=%v", result, err)
	}
	req := <-requests
	if req.Session == sess || req.Session.ID == sess.ID {
		t.Fatalf("side checker did not receive a session fork: %#v", req.Session)
	}
	history := req.Session.GetHistory()
	if len(history) != 1 || history[0].Role != types.RoleUser || history[0].Content != "parent context" {
		t.Fatalf("forked conversation = %#v, want parent context", history)
	}
	if !strings.Contains(req.UserMessage, "verify using the parent context") || !strings.Contains(req.UserMessage, "<side_check_execution>") {
		t.Fatalf("delegated check prompt = %q", req.UserMessage)
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

// loopLimitFakeAgent is a fakeAgent that exposes a loop ceiling like *agents.react.
type loopLimitFakeAgent struct {
	fakeAgent
	loops int
}

func (a *loopLimitFakeAgent) LoopLimit() int { return a.loops }

func TestChildTotalTimeoutFollowsLoopLimit(t *testing.T) {
	if _, ok := childTotalTimeout(&fakeAgent{response: "x"}); ok {
		t.Fatal("agents without LoopLimit must run under the parent context only")
	}
	previous := agents.PerLoopBudget
	agents.PerLoopBudget = 10 * time.Millisecond
	defer func() { agents.PerLoopBudget = previous }()

	timeout, ok := childTotalTimeout(&loopLimitFakeAgent{loops: 3})
	if !ok || timeout != 30*time.Millisecond {
		t.Fatalf("childTotalTimeout=%v ok=%v, want 30ms (3 loops x 10ms)", timeout, ok)
	}
}

func TestSubagentChildRunBoundedByTotalTimeout(t *testing.T) {
	previous := agents.PerLoopBudget
	agents.PerLoopBudget = 40 * time.Millisecond
	defer func() { agents.PerLoopBudget = previous }()

	hanging := &loopLimitFakeAgent{loops: 2}
	hanging.chatFunc = func(ctx context.Context, _ *api.Request) *api.Response {
		resp := api.NewResponse()
		go func() {
			// Never closes the response so the consumer deadline is the only exit.
			<-ctx.Done()
		}()
		return resp
	}

	sess := newTestSession(t)
	handler := callSubagentTool([]ExpertAgent{{Name: "worker", Agent: hanging}}, sess, nil)

	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	result, err := handler(parent, &tools.Request{
		SessionID: sess.Root.ID,
		Arguments: map[string]any{"tasks": []any{
			map[string]any{"agent_name": "worker", "task": "hang until bounded"},
		}},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("hanging child should fail the batch task: %+v", result)
	}
	// The child total timeout (2 loops x 40ms) must fire well before the 5s
	// parent deadline; otherwise the bound is not being applied.
	if elapsed >= time.Second {
		t.Fatalf("child run was not bounded by its total timeout (took %v)", elapsed)
	}
	if elapsed < 70*time.Millisecond {
		t.Fatalf("child run ended too early (%v), want ~80ms (2 loops x 40ms)", elapsed)
	}
	text := result.Content[0].(tools.TextContent).Text
	if !strings.Contains(text, "context deadline exceeded") {
		t.Fatalf("batch result should report the deadline, got: %s", text)
	}
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

func TestCallSideCheckTool_AntiNestingGuard(t *testing.T) {
	sess := newTestSession(t)
	sideChecker := &ExpertAgent{Name: "side_checker", Agent: &fakeAgent{response: "should-not-happen"}}
	handler := callSideCheckTool(sideChecker, sess, nil)

	req := &tools.Request{
		SessionID: "forked-" + sess.Root.ID,
		Arguments: map[string]interface{}{
			"checks": []any{"nested side check"},
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
