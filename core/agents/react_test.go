package agents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

func TestReactPersistsAssistantContentReasoningAndToolCallsInSingleMessage(t *testing.T) {
	llm := &fakeLLMClient{
		completions: [][]providers.Delta{
			{
				{Reasoning: "Need to inspect the file first."},
				{Content: "I will inspect the file."},
				{ToolUse: []providers.ToolCall{{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: `{"path":"core/session/compact.go"}`,
				}}},
			},
			{
				{Content: "Done."},
			},
		},
	}

	tool := tools.NewTool("read_file",
		tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			return tools.NewToolResultText("file content"), nil
		}),
	)

	sess := session.New("sess-react", llm)
	resp := New(llm, Option{SystemPrompt: "system prompt", MaxLoopTimes: 4}).Chat(context.Background(), &api.Request{
		Session:     sess,
		UserMessage: "Inspect the file.",
		Tools:       []*tools.Tool{tool},
	})

	if _, err := api.ReadAllContent(context.Background(), resp); err != nil {
		t.Fatalf("ReadAllContent() error = %v", err)
	}

	history := sess.GetHistory()
	if len(history) != 4 {
		t.Fatalf("expected 4 history messages, got %#v", history)
	}

	firstAssistant := history[1]
	if firstAssistant.Role != types.RoleAssistant {
		t.Fatalf("expected second message to be assistant, got %#v", firstAssistant)
	}
	if firstAssistant.Content != "I will inspect the file." {
		t.Fatalf("expected assistant content to be preserved, got %#v", firstAssistant)
	}
	if firstAssistant.Reasoning != "Need to inspect the file first." {
		t.Fatalf("expected assistant reasoning to be preserved, got %#v", firstAssistant)
	}
	if len(firstAssistant.ToolCalls) != 1 || firstAssistant.ToolCalls[0].Name != "read_file" {
		t.Fatalf("expected assistant tool call to be persisted on the same message, got %#v", firstAssistant)
	}

	if history[2].Role != types.RoleTool || history[2].ToolResult == nil {
		t.Fatalf("expected third message to be tool result, got %#v", history[2])
	}
	if history[3].Role != types.RoleAssistant || history[3].Content != "Done." {
		t.Fatalf("expected final assistant message, got %#v", history[3])
	}
}

func TestReactPreservesReasoningSignatureAndWhitespace(t *testing.T) {
	llm := &fakeLLMClient{
		completions: [][]providers.Delta{
			{
				{Reasoning: "  Need to inspect the file first.\n"},
				{ReasoningSignature: "sig-123"},
				{RedactedThinking: "opaque-redacted-payload"},
				{Content: "I will inspect the file."},
				{ToolUse: []providers.ToolCall{{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: `{"path":"core/session/compact.go"}`,
				}}},
			},
			{
				{Content: "Done."},
			},
		},
	}

	tool := tools.NewTool("read_file",
		tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			return tools.NewToolResultText("file content"), nil
		}),
	)

	sess := session.New("sess-react-thinking", llm)
	resp := New(llm, Option{SystemPrompt: "system prompt", MaxLoopTimes: 4}).Chat(context.Background(), &api.Request{
		Session:     sess,
		UserMessage: "Inspect the file.",
		Tools:       []*tools.Tool{tool},
	})

	if _, err := api.ReadAllContent(context.Background(), resp); err != nil {
		t.Fatalf("ReadAllContent() error = %v", err)
	}

	history := sess.GetHistory()
	if len(history) != 4 {
		t.Fatalf("expected 4 history messages, got %#v", history)
	}

	firstAssistant := history[1]
	if firstAssistant.Reasoning != "  Need to inspect the file first.\n" {
		t.Fatalf("expected assistant reasoning whitespace to be preserved, got %#v", firstAssistant.Reasoning)
	}
	if firstAssistant.ReasoningSignature != "sig-123" {
		t.Fatalf("expected assistant reasoning signature to be preserved, got %#v", firstAssistant.ReasoningSignature)
	}
	if firstAssistant.RedactedThinking != "opaque-redacted-payload" {
		t.Fatalf("expected assistant redacted thinking to be preserved, got %#v", firstAssistant.RedactedThinking)
	}
}

func TestCanonicalizeToolCallsMakesDuplicateFallbackIDsUnique(t *testing.T) {
	toolUses := canonicalizeToolCalls([]providers.ToolCall{
		{Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`},
		{Name: "read_file", Arguments: `{"path":"core/session/compact.go"}`},
	})

	if len(toolUses) != 2 {
		t.Fatalf("expected 2 tool calls, got %#v", toolUses)
	}
	if toolUses[0].ID == "" || toolUses[1].ID == "" {
		t.Fatalf("expected generated IDs, got %#v", toolUses)
	}
	if toolUses[0].ID == toolUses[1].ID {
		t.Fatalf("expected duplicate fallback IDs to be uniquified, got %#v", toolUses)
	}
}

type fakeLLMClient struct {
	mu          sync.Mutex
	completions [][]providers.Delta
	calls       int
}

func (f *fakeLLMClient) Completion(_ context.Context, _ providers.Request) providers.Response {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.mu.Unlock()

	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		if idx >= len(f.completions) {
			resp.Err <- errors.New("unexpected completion call")
			return
		}
		for _, delta := range f.completions[idx] {
			resp.Stream <- delta
		}
	}()
	return resp
}

func (f *fakeLLMClient) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeLLMClient) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("not implemented")
}

func TestReactCalibratesMessageTokensFromAPI(t *testing.T) {
	llm := &calibratingFakeLLM{
		completions: [][]providers.Delta{
			{{Content: "Hello back."}},
		},
		promptTokens:     150,
		completionTokens: 30,
	}

	sess := session.New("sess-cal", llm)
	resp := New(llm, Option{SystemPrompt: "system prompt", MaxLoopTimes: 4}).Chat(context.Background(), &api.Request{
		Session:     sess,
		UserMessage: "Hello world, this is a test message.",
	})

	if _, err := api.ReadAllContent(context.Background(), resp); err != nil {
		t.Fatalf("ReadAllContent() error = %v", err)
	}

	ctxState := sess.EnsureContextState()

	// Token checkpoint should be recorded with the LLM's actual prompt_tokens
	if ctxState.TokenCheckpoint.PromptTokens != 150 {
		t.Fatalf("expected TokenCheckpoint.PromptTokens=150, got %d", ctxState.TokenCheckpoint.PromptTokens)
	}

	// Checkpoint index should match history length at the time (1 user message)
	if ctxState.TokenCheckpoint.Index != 1 {
		t.Fatalf("expected TokenCheckpoint.Index=1, got %d", ctxState.TokenCheckpoint.Index)
	}

	// Session.Tokens() should return checkpoint base + estimated new messages
	// Checkpoint base=150, new messages = 1 assistant message ("Hello back.")
	totalTokens := sess.Tokens()
	if totalTokens <= 150 {
		t.Fatalf("expected total tokens > 150 (checkpoint base + new msg estimate), got %d", totalTokens)
	}
}

type calibratingFakeLLM struct {
	mu               sync.Mutex
	completions      [][]providers.Delta
	calls            int
	promptTokens     int64
	completionTokens int64
}

func (f *calibratingFakeLLM) Completion(_ context.Context, _ providers.Request) providers.Response {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.mu.Unlock()

	resp := providers.NewCommonResponse()
	resp.Token = providers.Tokens{
		PromptTokens:     f.promptTokens,
		CompletionTokens: f.completionTokens,
		TotalTokens:      f.promptTokens + f.completionTokens,
	}
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		if idx >= len(f.completions) {
			resp.Err <- errors.New("unexpected completion call")
			return
		}
		for _, delta := range f.completions[idx] {
			resp.Stream <- delta
		}
	}()
	return resp
}

func (f *calibratingFakeLLM) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("not implemented")
}

func (f *calibratingFakeLLM) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("not implemented")
}

// idleFakeLLM sends one delta then waits for cancellation, simulating a stuck LLM.
type idleFakeLLM struct {
	calls    int
	content  string
	canceled chan int
	mu       sync.Mutex
}

func (f *idleFakeLLM) Completion(ctx context.Context, _ providers.Request) providers.Response {
	f.mu.Lock()
	call := f.calls
	f.calls++
	f.mu.Unlock()

	content := f.content
	if content == "" {
		content = "partial"
	}
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)

		select {
		case resp.Stream <- providers.Delta{Content: content}:
		case <-ctx.Done():
			f.notifyCanceled(call)
			return
		}

		<-ctx.Done()
		f.notifyCanceled(call)
	}()
	return resp
}

func (f *idleFakeLLM) notifyCanceled(call int) {
	if f.canceled == nil {
		return
	}
	select {
	case f.canceled <- call:
	default:
	}
}

func (f *idleFakeLLM) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("not implemented")
}
func (f *idleFakeLLM) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("not implemented")
}

func TestReact_StreamIdleTimeout(t *testing.T) {
	llm := &idleFakeLLM{canceled: make(chan int, 8)}
	sess := session.New("sess-idle", llm)

	agent := New(llm, Option{
		SystemPrompt:      "system",
		MaxLoopTimes:      2,
		StreamIdleTimeout: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Say hi.",
	})
	// ReadAllContent returns when the response closes; the react loop should retry
	// and eventually exhaust MaxLoopTimes and close the response.
	_, _ = api.ReadAllContent(ctx, resp)

	// The agent should have invoked the LLM at least twice (1 retry after idle timeout).
	llm.mu.Lock()
	calls := llm.calls
	llm.mu.Unlock()
	if calls < 2 {
		t.Fatalf("expected at least 2 llm calls after idle timeout retry, got %d", calls)
	}
	waitForCanceledCall(t, llm.canceled, 0)
}

// emptyFakeLLM immediately closes the stream with zero tokens, simulating an empty response.
type emptyFakeLLM struct{}

func (f *emptyFakeLLM) Completion(_ context.Context, _ providers.Request) providers.Response {
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		// No deltas at all.
	}()
	return resp
}

func (f *emptyFakeLLM) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("not implemented")
}
func (f *emptyFakeLLM) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("not implemented")
}

func TestReact_EmptyResponseFallback(t *testing.T) {
	llm := &emptyFakeLLM{}
	sess := session.New("sess-empty", llm)
	resp := New(llm, Option{SystemPrompt: "system", MaxLoopTimes: 1}).Chat(context.Background(), &api.Request{
		Session:     sess,
		UserMessage: "Say hi.",
	})
	content, _ := api.ReadAllContent(context.Background(), resp)
	if !strings.Contains(content, "failed to generate a valid response") {
		t.Fatalf("expected fallback message, got: %q", content)
	}
	last := sess.GetHistory()[len(sess.GetHistory())-1]
	if last.Role != types.RoleAssistant || !strings.Contains(last.Content, "failed to generate") {
		t.Fatalf("expected fallback message persisted in history; got %#v", last)
	}
}

func TestReact_CancelsStreamWhenMaxTokensExceeded(t *testing.T) {
	llm := &idleFakeLLM{
		content:  strings.Repeat("x", 32),
		canceled: make(chan int, 1),
	}
	sess := session.New("sess-max-tokens", llm)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp := New(llm, Option{
		SystemPrompt: "system",
		MaxLoopTimes: 1,
		MaxTokens:    4,
	}).Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Say hi.",
	})
	content, err := api.ReadAllContent(ctx, resp)
	if err != nil {
		t.Fatalf("ReadAllContent() error = %v", err)
	}
	if !strings.Contains(content, "response interrupted because the model exceeded the configured max tokens") {
		t.Fatalf("expected max-tokens warning, got %q", content)
	}
	waitForCanceledCall(t, llm.canceled, 0)
}

func TestIsMaxTokensError(t *testing.T) {
	tests := []struct {
		errMsg string
		match  bool
	}{
		{"exceed max message tokens: 100000", true},
		{"context_length_exceeded: too many tokens", true},
		// DeepSeek / OpenAI-compatible (production incident wording)
		{"This model's maximum context length is 1048576 tokens. However, you requested 1057011 tokens (857011 in the messages, 200000 in the completion)", true},
		// vLLM v0.18+
		{"Input length (9231) exceeds model's maximum context length (8192).", true},
		// vLLM legacy engine
		{"The decoder prompt (length 9231) is longer than the maximum model length of 8192.", true},
		// Anthropic
		{"prompt is too long: 857011 tokens > 200000 maximum", true},
		// GLM/Zhipu error code 1261
		{"Prompt 超长", true},
		// Kimi
		{"Input token length too long", true},
		{"prompt tokens + max_tokens exceeds the model specification", true},
		// Ordinary 400 validation errors mentioning max_tokens / token limits
		// must NOT trigger compaction retries.
		{"Invalid parameter: 'max_tokens'", false},
		{"max_tokens: Field required", false},
		{"token limit set too low", false},
		{"token limit reached", false},
		{"request too large: max_tokens exceeded", false},
		{"network error", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isMaxTokensError(errors.New(tt.errMsg))
		if got != tt.match {
			t.Errorf("isMaxTokensError(%q) = %v, want %v", tt.errMsg, got, tt.match)
		}
	}
}

func TestIsStreamIdleTimeout(t *testing.T) {
	if !isStreamIdleTimeout(&StreamIdleTimeoutError{Timeout: time.Second, Received: 2}) {
		t.Fatalf("expected StreamIdleTimeoutError to be detected")
	}
	if isStreamIdleTimeout(errors.New("other")) {
		t.Fatalf("plain error should not match")
	}
	wrapped := errors.New("wrap: ") // not unwrapped
	_ = wrapped
	if isStreamIdleTimeout(errors.New("stream idle timeout: text")) {
		// Note: isStreamIdleTimeout uses errors.As, so a non-wrapped plain error
		// with the same text should NOT match. This confirms behavior.
		t.Fatalf("stringly-typed error should not be matched by errors.As")
	}
}

func waitForCanceledCall(t *testing.T, ch <-chan int, want int) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("expected canceled call %d, got %d", want, got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for canceled call %d", want)
	}
}

func TestReactChatPropagatesTurnMetadata(t *testing.T) {
	llm := &fakeLLMClient{completions: [][]providers.Delta{{{Content: "hello"}}}}
	sess := session.New("session-metadata", llm)
	metadata := map[string]string{"session_turn_id": "turn-123"}
	req := &api.Request{
		Session:     sess,
		UserMessage: "hello",
		Metadata:    metadata,
	}

	response := New(llm, Option{MaxLoopTimes: 1}).Chat(context.Background(), req)
	if _, err := api.ReadAllContent(context.Background(), response); err != nil {
		t.Fatalf("ReadAllContent() error = %v", err)
	}

	history := sess.GetHistory()
	if len(history) != 2 {
		t.Fatalf("expected user and assistant messages, got %d", len(history))
	}
	for _, message := range history {
		if message.Metadata["session_turn_id"] != "turn-123" {
			t.Fatalf("expected message turn metadata turn-123, got %#v", message.Metadata)
		}
	}

	metadata["session_turn_id"] = "caller-mutated"
	history[0].Metadata["session_turn_id"] = "user-mutated"
	if history[1].Metadata["session_turn_id"] != "turn-123" {
		t.Fatalf("expected assistant metadata to be independent, got %#v", history[1].Metadata)
	}
}

func TestTryToolCallReturnsInterruptedToolResultWhenContextCanceled(t *testing.T) {
	release := make(chan struct{})
	handlerDone := make(chan struct{})

	waitTool := tools.NewTool("wait_tool",
		tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			defer close(handlerDone)
			<-release
			return tools.NewToolResultText("finished"), nil
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	agt := &react{logger: logger.New("react-test")}
	sess := session.New("sess-tool-cancel", nil)

	msgs := agt.tryToolCall(ctx, sess, providers.ToolCall{
		ID:        "call-cancelled",
		Name:      "wait_tool",
		Arguments: `{}`,
	}, "", "", "", []*tools.Tool{waitTool}, nil)

	close(release)

	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("background tool handler did not exit")
	}

	if len(msgs) != 2 {
		t.Fatalf("expected assistant tool call plus interrupted tool result, got %#v", msgs)
	}
	if msgs[1] == nil || msgs[1].Role != types.RoleTool || msgs[1].ToolResult == nil {
		t.Fatalf("expected interrupted tool result message, got %#v", msgs)
	}
	if msgs[1].ToolResult.CallID != "call-cancelled" {
		t.Fatalf("expected call ID to be preserved, got %#v", msgs[1].ToolResult)
	}
	if !strings.Contains(msgs[1].ToolResult.Content, "interrupted before completion") {
		t.Fatalf("expected interruption marker in tool result, got %q", msgs[1].ToolResult.Content)
	}
}

func TestWaitForToolCallOutcomePrefersCompletedResultWhenContextAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan toolCallOutcome, 1)
	done <- toolCallOutcome{msg: "finished", success: true}

	outcome, ok := waitForToolCallOutcome(ctx, done)
	if !ok {
		t.Fatal("expected completed tool outcome to win over cancellation")
	}
	if outcome.msg != "finished" || !outcome.success || outcome.err != nil {
		t.Fatalf("unexpected outcome: %#v", outcome)
	}
}

func TestWaitForToolCallOutcomeReturnsInterruptedWhenToolStillRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome, ok := waitForToolCallOutcome(ctx, make(chan toolCallOutcome))
	if ok {
		t.Fatalf("expected cancellation when tool outcome is not ready, got %#v", outcome)
	}
}

func TestDoToolCallsAppendsInterruptedResultForCanceledTool(t *testing.T) {
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	slowDone := make(chan struct{})

	fastTool := tools.NewTool("fast_tool",
		tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			return tools.NewToolResultText("fast result"), nil
		}),
	)
	slowTool := tools.NewTool("slow_tool",
		tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			close(slowStarted)
			defer close(slowDone)
			<-releaseSlow
			return tools.NewToolResultText("slow result"), nil
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agt := &react{logger: logger.New("react-test")}
	sess := session.New("sess-tool-cancelled-batch", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		agt.doToolCalls(ctx, sess, []providers.ToolCall{
			{ID: "call-fast", Name: "fast_tool", Arguments: `{}`},
			{ID: "call-slow", Name: "slow_tool", Arguments: `{}`},
		}, "", "", "", []*tools.Tool{fastTool, slowTool}, nil)
	}()

	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow tool did not start")
	}

	deadline := time.Now().Add(time.Second)
	for {
		history := sess.GetHistory()
		if len(history) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fast tool result was not persisted before cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("doToolCalls did not return after cancellation")
	}

	close(releaseSlow)

	select {
	case <-slowDone:
	case <-time.After(time.Second):
		t.Fatal("slow tool handler did not exit")
	}

	history := sess.GetHistory()
	if len(history) != 2 {
		t.Fatalf("expected 2 tool results after cancellation, got %#v", history)
	}
	if history[0].ToolResult == nil || history[0].ToolResult.CallID != "call-fast" {
		t.Fatalf("expected fast tool result to stay paired, got %#v", history[0])
	}
	if history[1].ToolResult == nil || history[1].ToolResult.CallID != "call-slow" {
		t.Fatalf("expected interrupted slow tool result to be appended, got %#v", history[1])
	}
	if !strings.Contains(history[1].ToolResult.Content, "interrupted before completion") {
		t.Fatalf("expected interrupted slow tool result content, got %q", history[1].ToolResult.Content)
	}
}

func TestDoToolCallsAppendsInterruptedResultsForMultipleCanceledTools(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	doneCh := make(chan string, 3)

	makeBlockingTool := func(name string) *tools.Tool {
		return tools.NewTool(name,
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				started <- name
				defer func() { doneCh <- name }()
				<-release
				return tools.NewToolResultText(name + " result"), nil
			}),
		)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agt := &react{logger: logger.New("react-test")}
	sess := session.New("sess-tool-cancelled-multi", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		agt.doToolCalls(ctx, sess, []providers.ToolCall{
			{ID: "call-slow-1", Name: "slow_tool_1", Arguments: `{}`},
			{ID: "call-slow-2", Name: "slow_tool_2", Arguments: `{}`},
			{ID: "call-slow-3", Name: "slow_tool_3", Arguments: `{}`},
		}, "", "", "", []*tools.Tool{
			makeBlockingTool("slow_tool_1"),
			makeBlockingTool("slow_tool_2"),
			makeBlockingTool("slow_tool_3"),
		}, nil)
	}()

	seen := map[string]bool{}
	deadline := time.After(time.Second)
	for len(seen) < 3 {
		select {
		case name := <-started:
			seen[name] = true
		case <-deadline:
			t.Fatalf("expected all blocking tools to start, saw %#v", seen)
		}
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("doToolCalls did not return after multi-tool cancellation")
	}

	close(release)

	for i := 0; i < 3; i++ {
		select {
		case <-doneCh:
		case <-time.After(time.Second):
			t.Fatal("blocking tool handler did not exit")
		}
	}

	history := sess.GetHistory()
	if len(history) != 3 {
		t.Fatalf("expected 3 interrupted tool results after cancellation, got %#v", history)
	}

	wantIDs := []string{"call-slow-1", "call-slow-2", "call-slow-3"}
	for i, wantID := range wantIDs {
		if history[i].Role != types.RoleTool || history[i].ToolResult == nil {
			t.Fatalf("expected history[%d] to be a tool result, got %#v", i, history[i])
		}
		if history[i].ToolResult.CallID != wantID {
			t.Fatalf("expected history[%d] call ID %q, got %#v", i, wantID, history[i].ToolResult)
		}
		if !strings.Contains(history[i].ToolResult.Content, "interrupted before completion") {
			t.Fatalf("expected history[%d] to contain interruption marker, got %q", i, history[i].ToolResult.Content)
		}
	}
}

type modelCallRecorder struct {
	mu    sync.Mutex
	stats []*session.ModelCallStats
	fail  bool
}

func (r *modelCallRecorder) AfterModelCall(_ context.Context, _ *session.Session, _ providers.Request, stats *session.ModelCallStats) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats = append(r.stats, stats)
	if r.fail {
		return errors.New("hook failure must be swallowed")
	}
	return nil
}

func (r *modelCallRecorder) recorded() []*session.ModelCallStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*session.ModelCallStats(nil), r.stats...)
}

type namedFakeLLM struct {
	*calibratingFakeLLM
}

func (f *namedFakeLLM) ModelName() string { return "test-model-x" }

func TestReactFiresAfterModelCallOnSuccess(t *testing.T) {
	llm := &namedFakeLLM{&calibratingFakeLLM{
		completions:      [][]providers.Delta{{{Content: "Hello back."}}},
		promptTokens:     150,
		completionTokens: 30,
	}}
	rec := &modelCallRecorder{}
	sess := session.New("sess-model-call-success", llm, session.WithHooks(rec))
	resp := New(llm, Option{SystemPrompt: "system prompt", MaxLoopTimes: 1}).Chat(context.Background(), &api.Request{
		Session:     sess,
		UserMessage: "Hello world.",
	})
	if _, err := api.ReadAllContent(context.Background(), resp); err != nil {
		t.Fatalf("ReadAllContent() error = %v", err)
	}

	calls := rec.recorded()
	if len(calls) != 1 {
		t.Fatalf("expected 1 after_model_call hook fire, got %d", len(calls))
	}
	st := calls[0]
	if st.Err != "" {
		t.Fatalf("expected empty Err on success, got %q", st.Err)
	}
	if st.Model != "test-model-x" {
		t.Fatalf("expected model name test-model-x, got %q", st.Model)
	}
	if st.Tokens.PromptTokens != 150 || st.Tokens.CompletionTokens != 30 {
		t.Fatalf("expected tokens 150/30, got %#v", st.Tokens)
	}
	if st.Content != "Hello back." {
		t.Fatalf("expected captured content, got %q", st.Content)
	}
	if st.DurationMs < 0 {
		t.Fatalf("expected non-negative duration, got %d", st.DurationMs)
	}
}

func TestReactFiresAfterModelCallOnError(t *testing.T) {
	llm := &erroringFakeLLM{}
	rec := &modelCallRecorder{}
	sess := session.New("sess-model-call-error", llm, session.WithHooks(rec))
	resp := New(llm, Option{MaxLoopTimes: 1}).Chat(context.Background(), &api.Request{
		Session:     sess,
		UserMessage: "Hello world.",
	})
	if _, err := api.ReadAllContent(context.Background(), resp); err == nil {
		t.Fatal("expected chat to fail with LLM error")
	}

	calls := rec.recorded()
	if len(calls) != 1 {
		t.Fatalf("expected 1 after_model_call hook fire on error path, got %d", len(calls))
	}
	st := calls[0]
	if st.Err == "" {
		t.Fatal("expected Err to carry the LLM failure message")
	}
	if st.Tokens.PromptTokens != 0 {
		t.Fatalf("expected zero tokens on error, got %#v", st.Tokens)
	}
}

func TestReactSwallowsAfterModelCallHookError(t *testing.T) {
	llm := &namedFakeLLM{&calibratingFakeLLM{
		completions:      [][]providers.Delta{{{Content: "ok"}}},
		promptTokens:     10,
		completionTokens: 5,
	}}
	rec := &modelCallRecorder{fail: true}
	sess := session.New("sess-model-call-hook-err", llm, session.WithHooks(rec))
	resp := New(llm, Option{MaxLoopTimes: 1}).Chat(context.Background(), &api.Request{
		Session:     sess,
		UserMessage: "Hello world.",
	})
	if _, err := api.ReadAllContent(context.Background(), resp); err != nil {
		t.Fatalf("expected hook error to be swallowed, got chat error = %v", err)
	}
	if len(rec.recorded()) != 1 {
		t.Fatalf("expected hook to fire once, got %d", len(rec.recorded()))
	}
}

type erroringFakeLLM struct{}

func (f *erroringFakeLLM) Completion(_ context.Context, _ providers.Request) providers.Response {
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		resp.Err <- errors.New("boom: provider unavailable")
	}()
	return resp
}

func (f *erroringFakeLLM) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("not implemented")
}

func (f *erroringFakeLLM) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("not implemented")
}

// maxTokensErrLLM fails every completion with a context-overflow error.
type maxTokensErrLLM struct {
	calls int32
}

func (f *maxTokensErrLLM) Completion(_ context.Context, _ providers.Request) providers.Response {
	atomic.AddInt32(&f.calls, 1)
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		resp.Err <- errors.New("This model's maximum context length is 8192 tokens. However, you requested 10000 tokens")
	}()
	return resp
}

func (f *maxTokensErrLLM) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("not implemented")
}

func (f *maxTokensErrLLM) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("not implemented")
}

// TestReactMaxTokensErrorCompactRetryIsBounded guards against the unbounded
// compact-retry loop: when every attempt fails with a max-tokens error and
// compaction keeps "succeeding", the loop must stop at MaxLoopTimes.
func TestReactMaxTokensErrorCompactRetryIsBounded(t *testing.T) {
	llm := &maxTokensErrLLM{}
	// Session LLM is nil so CompactHistory falls back to truncation and
	// always returns nil, keeping the retry path alive.
	sess := session.New("sess-max-tokens-retry", nil)
	agent := New(llm, Option{SystemPrompt: "system", MaxLoopTimes: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := agent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Say hi.",
	})
	if _, err := api.ReadAllContent(ctx, resp); err == nil {
		t.Fatal("expected chat to fail with the max-tokens error instead of looping forever")
	}

	calls := atomic.LoadInt32(&llm.calls)
	// 1 initial call + MaxLoopTimes compact-retries (the bound fails the last one).
	if calls > 4 {
		t.Fatalf("expected at most %d llm calls, got %d", 4, calls)
	}
	if calls < 2 {
		t.Fatalf("expected at least one compact-retry, got %d calls", calls)
	}
}

// tokenRacingLLM streams one delta and then keeps accumulating token usage
// until its context is cancelled, so usage writes race the agent's early-exit
// token snapshot reads. Run with -race.
type tokenRacingLLM struct{}

func (f *tokenRacingLLM) Completion(ctx context.Context, _ providers.Request) providers.Response {
	resp := providers.NewCommonResponse()
	go func() {
		defer close(resp.Stream)
		defer close(resp.Err)
		resp.AddTokens(providers.Tokens{PromptTokens: 100})
		resp.Stream <- providers.Delta{Content: "partial"}
		for {
			resp.AddTokens(providers.Tokens{CompletionTokens: 1})
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return resp
}

func (f *tokenRacingLLM) CompletionNonStreaming(context.Context, providers.Request) (string, error) {
	return "", errors.New("not implemented")
}

func (f *tokenRacingLLM) StructuredPredict(context.Context, providers.Request, any) error {
	return errors.New("not implemented")
}

func TestReactTokenSnapshotOnEarlyExit(t *testing.T) {
	llm := &tokenRacingLLM{}
	rec := &modelCallRecorder{}
	sess := session.New("sess-token-race", llm, session.WithHooks(rec))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	resp := New(llm, Option{SystemPrompt: "system", MaxLoopTimes: 1}).Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Say hi.",
	})
	// The context deadline aborts the model call; the snapshot reads in the
	// early-exit path (fireModelCall) must be race-free against the still
	// running provider goroutine.
	_, _ = api.ReadAllContent(context.Background(), resp)

	calls := rec.recorded()
	if len(calls) != 1 {
		t.Fatalf("expected 1 after_model_call hook fire on early exit, got %d", len(calls))
	}
	if calls[0].Err == "" {
		t.Fatal("expected the cancellation to be reported in stats.Err")
	}
}

func TestInterruptedToolResultMessageDoesNotSuggestRetry(t *testing.T) {
	for _, msg := range []string{
		interruptedToolResultMessage(nil),
		interruptedToolResultMessage(context.Canceled),
	} {
		if !strings.Contains(msg, "interrupted before completion") {
			t.Fatalf("expected interruption marker, got %q", msg)
		}
		if strings.Contains(msg, "retry") {
			t.Fatalf("interrupted tool result must not encourage a blind retry: %q", msg)
		}
		if !strings.Contains(msg, "unknown") {
			t.Fatalf("expected the message to state the effect is unknown, got %q", msg)
		}
	}
}
