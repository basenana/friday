package agents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
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

func TestIsContextWindowExceeded(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("network blip"), false},
		{"openai style", errors.New("This model's maximum context length is 8192 tokens"), true},
		{"deepseek style", errors.New("exceed max message tokens: 64000"), true},
		{"anthropic style", errors.New("prompt is too long: 300000 > 200000"), true},
		{"generic context_length_exceeded", errors.New("context_length_exceeded"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isContextWindowExceeded(tc.err); got != tc.want {
				t.Fatalf("isContextWindowExceeded(%v)=%v want %v", tc.err, got, tc.want)
			}
		})
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
