package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInvokerClassifiesInvocationDeadlines(t *testing.T) {
	tests := []struct {
		name       string
		invoker    *Invoker
		tool       *Tool
		context    func() (context.Context, context.CancelFunc)
		wantKind   TimeoutKind
		wantMillis int64
	}{
		{
			name:       "declared timeout",
			invoker:    NewInvoker(WithInvokerHardLimit(time.Second)),
			tool:       blockingTool("declared", 25*time.Millisecond),
			context:    backgroundContext,
			wantKind:   TimeoutKindDeclared,
			wantMillis: 25,
		},
		{
			name:       "hard limit",
			invoker:    NewInvoker(WithInvokerHardLimit(25 * time.Millisecond)),
			tool:       blockingTool("hard", 0),
			context:    backgroundContext,
			wantKind:   TimeoutKindHardLimit,
			wantMillis: 25,
		},
		{
			name:    "parent deadline",
			invoker: NewInvoker(WithInvokerHardLimit(time.Second)),
			tool:    blockingTool("parent", 500*time.Millisecond),
			context: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 25*time.Millisecond)
			},
			wantKind: TimeoutKindParentDeadline,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.context()
			defer cancel()
			started := time.Now()
			result, err := test.invoker.Invoke(ctx, test.tool, &Request{})
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if result == nil || !result.IsError || !result.TimedOut || result.Status != ResultStatusTimedOut {
				t.Fatalf("Invoke() result = %+v, want structured timeout", result)
			}
			if result.TimeoutKind != test.wantKind || result.ErrorCode != "tool_timeout" {
				t.Fatalf("timeout metadata = %+v, want kind %q", result, test.wantKind)
			}
			if test.wantMillis != 0 && result.TimeoutMs != test.wantMillis {
				t.Fatalf("TimeoutMs = %d, want %d", result.TimeoutMs, test.wantMillis)
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("Invoke() returned after %s", elapsed)
			}
		})
	}
}

func TestInvokerRejectsDeclaredTimeoutAboveMaximumWithoutCallingHandler(t *testing.T) {
	called := false
	tool := NewTool("limited",
		WithToolTimeout(time.Minute, "timeout"),
		WithToolHandler(func(context.Context, *Request) (*Result, error) {
			called = true
			return NewToolResultText("unexpected"), nil
		}),
	)

	result, err := NewInvoker().Invoke(context.Background(), tool, &Request{Arguments: map[string]interface{}{
		"timeout": "15m1s",
	}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if called {
		t.Fatal("handler was called for an invalid timeout")
	}
	if result == nil || !result.IsError || result.ErrorCode != "invalid_argument" || result.Status != ResultStatusError {
		t.Fatalf("Invoke() result = %+v, want invalid_argument", result)
	}
	if !strings.Contains(resultText(result), "must not exceed 15m0s") {
		t.Fatalf("result text = %q", resultText(result))
	}
}

func TestInvokerReturnsStructuredCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tool := NewTool("cancelled", WithToolHandler(func(ctx context.Context, _ *Request) (*Result, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}))

	result, err := NewInvoker().Invoke(ctx, tool, &Request{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result == nil || !result.IsError || !result.Cancelled || result.TimedOut || result.Status != ResultStatusCancelled || result.ErrorCode != "tool_cancelled" {
		t.Fatalf("Invoke() result = %+v, want structured cancellation", result)
	}
}

func TestInvokerDoesNotCallHandlerForCancelledParent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	tool := NewTool("cancelled", WithToolHandler(func(context.Context, *Request) (*Result, error) {
		called = true
		return NewToolResultText("unexpected"), nil
	}))

	result, err := NewInvoker().Invoke(ctx, tool, &Request{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if called || result == nil || !result.Cancelled || result.Status != ResultStatusCancelled {
		t.Fatalf("called = %v, result = %+v", called, result)
	}
}

func TestInvokerNormalizesLegacyResultStatus(t *testing.T) {
	tests := []struct {
		name       string
		result     *Result
		wantStatus ResultStatus
		wantError  bool
		wantTimed  bool
	}{
		{name: "success", result: &Result{}, wantStatus: ResultStatusSuccess},
		{name: "error", result: &Result{IsError: true}, wantStatus: ResultStatusError, wantError: true},
		{name: "timed out flag", result: &Result{TimedOut: true}, wantStatus: ResultStatusTimedOut, wantError: true, wantTimed: true},
		{name: "timed out status", result: &Result{Status: ResultStatusTimedOut}, wantStatus: ResultStatusTimedOut, wantError: true, wantTimed: true},
		{name: "timed out status and legacy error flag", result: &Result{Status: ResultStatusTimedOut, IsError: true}, wantStatus: ResultStatusTimedOut, wantError: true, wantTimed: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := NewTool("legacy", WithToolHandler(func(context.Context, *Request) (*Result, error) {
				return test.result, nil
			}))
			result, err := NewInvoker().Invoke(context.Background(), tool, &Request{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.wantStatus || result.IsError != test.wantError || result.TimedOut != test.wantTimed {
				t.Fatalf("normalized result = %+v", result)
			}
		})
	}
}

func TestInvokerPreservesHandlerErrorBeforeDeadline(t *testing.T) {
	want := errors.New("handler failed")
	tool := NewTool("failure", WithToolHandler(func(context.Context, *Request) (*Result, error) {
		return nil, want
	}))
	result, err := NewInvoker(WithInvokerHardLimit(time.Second)).Invoke(context.Background(), tool, &Request{})
	if result != nil || !errors.Is(err, want) {
		t.Fatalf("Invoke() = (%+v, %v), want handler error", result, err)
	}
}

func blockingTool(name string, timeout time.Duration) *Tool {
	options := []ToolOption{WithToolHandler(func(ctx context.Context, _ *Request) (*Result, error) {
		<-ctx.Done()
		exitCode := 124
		return &Result{ExitCode: &exitCode}, ctx.Err()
	})}
	if timeout > 0 {
		options = append(options, WithToolTimeout(timeout, "timeout"))
	}
	return NewTool(name, options...)
}

func backgroundContext() (context.Context, context.CancelFunc) {
	return context.Background(), func() {}
}

func resultText(result *Result) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	content, _ := result.Content[0].(TextContent)
	return content.Text
}
