package codebase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sandbox"
)

// stubProvider streams one configured completion per call, mirroring the way
// core/agents/react consumes provider output. The first call may instead request
// a tool call, which exercises the Codebase tool accounting end to end.
type stubProvider struct {
	chunks           []string
	firstToolCalls   []providers.ToolCall
	promptTokens     int64
	completionTokens int64

	mu    sync.Mutex
	calls int
	seen  []providers.Request
}

func (s *stubProvider) Completion(ctx context.Context, request providers.Request) providers.Response {
	resp := providers.NewCommonResponse()
	resp.Token = providers.Tokens{
		PromptTokens:     s.promptTokens,
		CompletionTokens: s.completionTokens,
		TotalTokens:      s.promptTokens + s.completionTokens,
	}
	s.mu.Lock()
	chunks := append([]string(nil), s.chunks...)
	if s.calls == 0 && len(s.firstToolCalls) > 0 {
		chunks = nil
	}
	s.calls++
	s.seen = append(s.seen, request)
	toolCalls := s.firstToolCalls
	toolCalls = append([]providers.ToolCall(nil), toolCalls...)
	first := s.calls == 1
	s.mu.Unlock()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		if first && len(toolCalls) > 0 {
			select {
			case resp.Stream <- providers.Delta{ToolUse: toolCalls}:
			case <-ctx.Done():
			}
			return
		}
		for _, chunk := range chunks {
			select {
			case resp.Stream <- providers.Delta{Content: chunk}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return resp
}

func (s *stubProvider) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", nil
}

func (s *stubProvider) StructuredPredict(context.Context, providers.Request, any) error { return nil }

func (s *stubProvider) requests() []providers.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]providers.Request(nil), s.seen...)
}

func newRunnerTestFixture(t *testing.T, client providers.Client) *runner {
	t.Helper()
	codebaseDir := filepath.Join(t.TempDir(), "codebase")
	if err := os.MkdirAll(codebaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Name: "test", Client: client}})
	return &runner{pool: pool, projectRoot: t.TempDir(), codebaseDir: codebaseDir}
}

func automaticSpec(maxLoopTimes int, maxOutputTokens int64) Spec {
	return Spec{
		Version: 1,
		Index:   modeSpec{Effort: "default", MaxLoopTimes: 100, MaxOutputTokens: 8192},
		Context: contextSpec{
			modeSpec: modeSpec{Effort: "none", MaxLoopTimes: maxLoopTimes, MaxOutputTokens: maxOutputTokens},
			Timeout:  duration{time.Minute},
		},
	}
}

func TestAutomaticContextUsesSpecOutputBudgetWithoutHiddenCeiling(t *testing.T) {
	const endMarker = "KB-END"
	content := strings.Repeat("knowledge line\n", 1000) + endMarker
	client := &stubProvider{chunks: []string{content}, promptTokens: 2100, completionTokens: 700}
	r := newRunnerTestFixture(t, client)

	out, stats, err := r.runContext(context.Background(), coresession.New("root", nil), []types.Message{{Role: types.RoleUser, Content: "question"}}, automaticSpec(10, 10000), "metadata")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, endMarker) || strings.Contains(out, "truncated by Codebase") {
		t.Fatalf("automatic Context output was shortened below the spec budget (%d chars): %q", len(out), out[max(0, len(out)-60):])
	}
	if stats.OutputChars != len(out) {
		t.Fatalf("stats output chars=%d, want %d", stats.OutputChars, len(out))
	}
	if stats.ModelCalls != 1 || stats.Truncated || stats.LoopLimit {
		t.Fatalf("stats=%+v", stats)
	}
	if stats.PromptTokensLast != 2100 || stats.CompletionTokensLast != 700 {
		t.Fatalf("stats tokens=%+v", stats)
	}
}

func TestAutomaticContextBoundsOutputAtSpecCeiling(t *testing.T) {
	client := &stubProvider{chunks: []string{strings.Repeat("x", 60_000)}}
	r := newRunnerTestFixture(t, client)

	const maxOutputTokens = 10_000
	out, stats, err := r.runContext(context.Background(), coresession.New("root", nil), nil, automaticSpec(10, maxOutputTokens), "metadata")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[truncated by Codebase]") {
		t.Fatalf("spec ceiling did not bound the output (%d chars)", len(out))
	}
	if limit := int(maxOutputTokens) * 4; len(out) > limit {
		t.Fatalf("output bytes=%d exceed spec-derived limit=%d", len(out), limit)
	}
	if !stats.Truncated || stats.OutputChars != len(out) {
		t.Fatalf("stats=%+v output=%d chars", stats, len(out))
	}
}

func TestAutomaticContextFlagsLoopLimitAtSpecBound(t *testing.T) {
	client := &stubProvider{chunks: []string{"knowledge summary"}}
	r := newRunnerTestFixture(t, client)

	out, stats, err := r.runContext(context.Background(), coresession.New("root", nil), nil, automaticSpec(1, 10000), "metadata")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if stats.ModelCalls != 1 || !stats.LoopLimit {
		t.Fatalf("stats=%+v, want one model call at the loop bound", stats)
	}
}

func TestFinalizeRunStatsDetectsTruncationAndLoopLimit(t *testing.T) {
	const warning = "\n\n[Warning: response interrupted because the model exceeded the configured max tokens (10000). The above may be incomplete or repetitive.]"

	if got := finalizeRunStats(runStats{ModelCalls: 3, ToolCalls: 2}, "answer"+warning, 10); !got.Truncated || got.LoopLimit {
		t.Fatalf("interrupted output not reported: %+v", got)
	}
	if got := finalizeRunStats(runStats{ModelCalls: 3}, "answer", 10); got.Truncated {
		t.Fatalf("complete output reported as truncated: %+v", got)
	}
	if got := finalizeRunStats(runStats{ModelCalls: 10}, "answer", 10); !got.LoopLimit {
		t.Fatalf("loop bound not reported: %+v", got)
	}
	if got := finalizeRunStats(runStats{ModelCalls: 9}, "answer", 10); got.LoopLimit {
		t.Fatalf("loop bound reported too early: %+v", got)
	}
}

func TestContextUsageProxyAccumulatesModelCalls(t *testing.T) {
	root := coresession.New("root", nil)
	proxy := &contextUsageProxy{root: root}
	for call := 0; call < 2; call++ {
		stats := &coresession.ModelCallStats{Tokens: providers.Tokens{PromptTokens: int64(100 + call), CompletionTokens: int64(10 + call)}}
		if err := proxy.AfterModelCall(context.Background(), root, nil, stats); err != nil {
			t.Fatal(err)
		}
	}
	got := proxy.snapshot()
	if got.ModelCalls != 2 || got.PromptTokensLast != 101 || got.CompletionTokensLast != 11 {
		t.Fatalf("stats=%+v", got)
	}
}

func TestContextUsageProxyCountsReadsOutsideKnowledgeBase(t *testing.T) {
	projectRoot := t.TempDir()
	codebaseDir := filepath.Join(projectRoot, ".friday", "codebase")
	proxy := &contextUsageProxy{root: coresession.New("root", nil), projectRoot: projectRoot, codebaseDir: codebaseDir}
	payloads := []providers.ToolCall{
		{Name: sandbox.FsReadToolName, Arguments: `{"path":"` + filepath.Join(codebaseDir, "INDEX.md") + `"}`},
		{Name: sandbox.FsListToolName, Arguments: `{"path":"` + filepath.Join(codebaseDir, "knowledge") + `"}`},
		{Name: sandbox.FsReadToolName, Arguments: `{"path":"internal/pdf/upload.go"}`},
		{Name: sandbox.FsSearchToolName, Arguments: `{"directory":"` + projectRoot + `","regex":"channel"}`},
		{Name: "codebase_git_history", Arguments: `{"operation":"log"}`},
		{Name: sandbox.FsReadToolName, Arguments: `{"path":"docs/superpowers/plan.md"}`},
		{Name: sandbox.FsReadToolName, Arguments: `{"path":"main.go"}`},
	}
	executions := make([]coresession.ToolExecution, 0, len(payloads))
	for _, call := range payloads {
		executions = append(executions, coresession.ToolExecution{Call: call})
	}
	if err := proxy.AfterTool(context.Background(), nil, coresession.ToolPayload{Executions: executions}); err != nil {
		t.Fatal(err)
	}

	got := proxy.snapshot()
	if got.ToolCalls != len(payloads) {
		t.Fatalf("tool calls=%d, want %d", got.ToolCalls, len(payloads))
	}
	if got.OutsideKBReads != 4 {
		t.Fatalf("outside knowledge base reads=%d samples=%v", got.OutsideKBReads, got.OutsideKBSamples)
	}
	if len(got.OutsideKBSamples) != maxOutsideKBSamples {
		t.Fatalf("samples=%v, want %d", got.OutsideKBSamples, maxOutsideKBSamples)
	}
	for _, sample := range got.OutsideKBSamples {
		if strings.Contains(sample, codebaseDir) {
			t.Fatalf("knowledge base read sampled as outside: %q", sample)
		}
	}
}

func TestAutomaticContextRequestsKnowledgeBaseOrientedInput(t *testing.T) {
	client := &stubProvider{chunks: []string{"knowledge summary"}}
	runtime, _ := newRuntimeTestFixtureWithClient(t, client)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ensureRunner(); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.enabled = true
	runtime.mu.Unlock()

	out, err := runtime.contextEvidence(context.Background(), coresession.New("root", nil), []types.Message{{Role: types.RoleUser, Content: "How does PDF upload work?"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "knowledge summary") {
		t.Fatalf("evidence=%q", out)
	}
	requests := client.requests()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d, want 1", len(requests))
	}
	if prompt := requests[0].SystemPrompt(); !strings.Contains(prompt, "Do not explore the repository") || !strings.Contains(prompt, "state the coverage gap") {
		t.Fatalf("automatic Context prompt is not knowledge-base oriented:\n%s", prompt)
	}
	if !requestHistoryHas(requests[0], "Summarize the maintained Codebase knowledge relevant to the current projected conversation.") {
		t.Fatalf("automatic Context input is not knowledge-base oriented: %+v", requests[0].History())
	}
	if !requestHistoryHas(requests[0], "Project: "+runtime.opts.Project.Root()) {
		t.Fatalf("automatic Context input lost its metadata: %+v", requests[0].History())
	}
}

func requestHistoryHas(req providers.Request, want string) bool {
	for _, message := range req.History() {
		if strings.Contains(message.Content, want) {
			return true
		}
	}
	return false
}

// captureLogger records structured log lines emitted by any logger resolved from
// the installed root, tagged with the logger name for filtering.
type captureLogger struct {
	logger.Logger

	name    string
	mu      *sync.Mutex
	entries *[]capturedLog
}

type capturedLog struct {
	name string
	msg  string
	keys map[string]any
}

func newCaptureLogger() *captureLogger {
	return &captureLogger{name: "root", mu: &sync.Mutex{}, entries: &[]capturedLog{}}
}

func (l *captureLogger) Named(name string) logger.Logger {
	return &captureLogger{name: name, mu: l.mu, entries: l.entries}
}

func (l *captureLogger) Infow(msg string, keysAndValues ...interface{}) {
	keys := map[string]any{}
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		keys[fmt.Sprint(keysAndValues[i])] = keysAndValues[i+1]
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.entries = append(*l.entries, capturedLog{name: l.name, msg: msg, keys: keys})
}

func (l *captureLogger) captured(name string) []capturedLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]capturedLog, 0, len(*l.entries))
	for _, entry := range *l.entries {
		if entry.name == name {
			out = append(out, entry)
		}
	}
	return out
}

func TestAutomaticContextLogsStartAndFinishRunLines(t *testing.T) {
	capture := newCaptureLogger()
	previous := logger.Root()
	logger.SetRoot(capture)
	defer logger.SetRoot(previous)

	client := &stubProvider{chunks: []string{"knowledge summary"}, promptTokens: 120, completionTokens: 8}
	runtime, _ := newRuntimeTestFixtureWithClient(t, client)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ensureRunner(); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.enabled = true
	runtime.mu.Unlock()

	root := coresession.New("root", nil)
	out, err := runtime.contextEvidence(context.Background(), root, []types.Message{{Role: types.RoleUser, Content: "question"}}, 3)
	if err != nil || !strings.Contains(out, "knowledge summary") {
		t.Fatalf("evidence=%q err=%v", out, err)
	}

	entries := capture.captured("codebase")
	if len(entries) != 2 {
		t.Fatalf("run log lines=%d, want a start and a finish line: %+v", len(entries), entries)
	}
	start, finish := entries[0], entries[1]
	if start.msg != "codebase run start" || finish.msg != "codebase run finish" {
		t.Fatalf("unexpected messages: %q, %q", start.msg, finish.msg)
	}
	for _, required := range []string{"mode", "session", "operation", "turn"} {
		if start.keys[required] != finish.keys[required] {
			t.Fatalf("%s differs between start and finish: %#v / %#v", required, start.keys, finish.keys)
		}
	}
	if start.keys["mode"] != "context" || start.keys["session"] != root.ID || start.keys["turn"] != uint64(3) {
		t.Fatalf("start line=%#v", start.keys)
	}
	if start.keys["max_output_tokens"] != int64(10000) || start.keys["max_loop_times"] != 10 {
		t.Fatalf("start line did not report the spec budget: %#v", start.keys)
	}
	for key, want := range map[string]any{
		"state": "ready", "model_calls": 1, "tool_calls": 0,
		"outside_kb_reads": 0, "truncated": false, "loop_limit": false,
		"prompt_tokens_last": int64(120), "completion_tokens_last": int64(8), "err": "",
	} {
		if finish.keys[key] != want {
			t.Fatalf("finish line %s=%#v, want %#v: %#v", key, finish.keys[key], want, finish.keys)
		}
	}
	if chars, ok := finish.keys["output_chars"].(int); !ok || chars <= 0 {
		t.Fatalf("finish line output_chars=%#v", finish.keys["output_chars"])
	}
	if _, ok := finish.keys["duration_ms"]; !ok {
		t.Fatalf("finish line is missing duration_ms: %#v", finish.keys)
	}
}

func TestAutomaticContextLogsToolCallsOutsideKnowledgeBase(t *testing.T) {
	capture := newCaptureLogger()
	previous := logger.Root()
	logger.SetRoot(capture)
	defer logger.SetRoot(previous)

	client := &stubProvider{
		chunks:         []string{"knowledge summary"},
		firstToolCalls: []providers.ToolCall{{ID: "call-1", Name: sandbox.FsReadToolName, Arguments: `{"path":"README.md"}`}},
	}
	runtime, _ := newRuntimeTestFixtureWithClient(t, client)
	if err := runtime.store.ensureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ensureRunner(); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.enabled = true
	runtime.mu.Unlock()
	if err := os.WriteFile(filepath.Join(runtime.opts.Project.Root(), "README.md"), []byte("project readme"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runtime.contextEvidence(context.Background(), coresession.New("root", nil), []types.Message{{Role: types.RoleUser, Content: "question"}}, 1)
	if err != nil || !strings.Contains(out, "knowledge summary") {
		t.Fatalf("evidence=%q err=%v", out, err)
	}

	entries := capture.captured("codebase")
	if len(entries) != 2 {
		t.Fatalf("run log lines=%d, want 2: %+v", len(entries), entries)
	}
	finish := entries[1]
	if finish.keys["model_calls"] != 2 || finish.keys["tool_calls"] != 1 {
		t.Fatalf("finish line did not account for the tool call: %#v", finish.keys)
	}
	if finish.keys["outside_kb_reads"] != 1 {
		t.Fatalf("repository read was not counted as outside the knowledge base: %#v", finish.keys)
	}
	if samples, _ := finish.keys["outside_kb_samples"].(string); !strings.Contains(samples, sandbox.FsReadToolName+" README.md") {
		t.Fatalf("outside knowledge base sample=%q", samples)
	}
}
