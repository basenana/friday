package setup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/tools"
)

func TestWrapToolsWithTrace_EmitsStartAndFailureEvents(t *testing.T) {
	var events []ToolTraceEvent
	wrapped := wrapToolsWithTrace([]*tools.Tool{
		tools.NewTool("bash",
			tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
				return tools.NewToolResultError("permission denied"), nil
			}),
		),
	}, func(event ToolTraceEvent) {
		events = append(events, event)
	})

	_, err := wrapped[0].Handler(context.Background(), &tools.Request{
		Arguments: map[string]interface{}{"command": "echo 1"},
	})
	if err != nil {
		t.Fatalf("wrapped tool returned unexpected error: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 trace events, got %#v", events)
	}
	if events[0].Kind != ToolTraceStart || events[0].Name != "bash" {
		t.Fatalf("unexpected start event: %#v", events[0])
	}
	if events[1].Kind != ToolTraceError || events[1].Error == "" {
		t.Fatalf("unexpected error event: %#v", events[1])
	}
}

func TestToolTracePreservesExplicitRetryEvidence(t *testing.T) {
	var events []ToolTraceEvent
	wrapped := wrapToolsWithTrace([]*tools.Tool{
		tools.NewTool("test", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
			return tools.NewToolResultText("ok"), nil
		})),
	}, func(event ToolTraceEvent) {
		events = append(events, event)
	})
	required := true
	ctx := WithToolInvocationEvidence(context.Background(), ToolInvocationEvidence{Attempt: 2, RetryOfInvocationID: "previous", Required: &required, RequiredSource: "EXECUTION_PLAN"})
	if _, err := wrapped[0].Handler(ctx, &tools.Request{}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two trace events, got %d", len(events))
	}
	for _, event := range events {
		if event.Attempt != 2 || event.RetryOfInvocationID != "previous" || event.Required == nil || !*event.Required || event.RequiredSource != "EXECUTION_PLAN" {
			t.Fatalf("explicit retry evidence was not preserved: %+v", event)
		}
	}
}

func TestInvalidRetryEvidenceIsRejectedWithoutRewriting(t *testing.T) {
	handlerCalled := false
	var events []ToolTraceEvent
	wrapped := wrapToolsWithTrace([]*tools.Tool{
		tools.NewTool("bash", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
			handlerCalled = true
			return tools.NewToolResultText("unexpected"), nil
		})),
	}, func(event ToolTraceEvent) { events = append(events, event) })
	ctx := WithToolInvocationEvidence(context.Background(), ToolInvocationEvidence{Attempt: 3})
	if _, err := wrapped[0].Handler(ctx, &tools.Request{}); err == nil {
		t.Fatal("invalid retry evidence must fail")
	}
	if handlerCalled {
		t.Fatal("invalid retry evidence must not execute the tool handler")
	}
	if len(events) != 2 || events[0].Attempt != 3 || events[1].Attempt != 3 {
		t.Fatalf("invalid retry evidence was rewritten or dropped: %+v", events)
	}
}

func TestInvocationRetryPolicyProducesProgramAndHPCRetryChains(t *testing.T) {
	for _, toolName := range []string{"bash", "submit_hpc_job"} {
		t.Run(toolName, func(t *testing.T) {
			calls := 0
			var observed []ToolInvocationEvidence
			var events []ToolTraceEvent
			base := []*tools.Tool{tools.NewTool(toolName, tools.WithToolHandler(func(ctx context.Context, _ *tools.Request) (*tools.Result, error) {
				calls++
				observed = append(observed, InvocationEvidenceFromContext(ctx))
				if calls == 1 {
					return tools.NewToolResultRetryableError("retryable failure", "transient test failure"), nil
				}
				return tools.NewToolResultText("ok"), nil
			}))}
			traced := wrapToolsWithTrace(base, func(event ToolTraceEvent) { events = append(events, event) })
			required := true
			wrapped := wrapToolsWithInvocationRetry(traced, func(string) ToolInvocationPolicy {
				return ToolInvocationPolicy{MaxAttempts: 2, Required: &required, RequiredSource: "EXECUTION_PLAN"}
			})
			result, err := wrapped[0].Handler(context.Background(), &tools.Request{})
			if err != nil || result == nil || result.IsError {
				t.Fatalf("retry did not complete successfully: result=%+v err=%v", result, err)
			}
			if calls != 2 || len(observed) != 2 {
				t.Fatalf("expected two real handler attempts, calls=%d observed=%+v", calls, observed)
			}
			if observed[0].Attempt != 1 || observed[0].RetryOfInvocationID != "" || observed[1].Attempt != 2 || observed[1].RetryOfInvocationID != observed[0].InvocationID {
				t.Fatalf("unexpected retry chain: %+v", observed)
			}
			if len(events) != 4 || events[0].Attempt != 1 || events[2].Attempt != 2 || events[2].RetryOfInvocationID != events[0].InvocationID {
				t.Fatalf("trace did not preserve retry chain: %+v", events)
			}
		})
	}
}

func TestInvocationRetryDefaultsToNoRetry(t *testing.T) {
	calls := 0
	base := []*tools.Tool{tools.NewTool("submit_hpc_job", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
		calls++
		return tools.NewToolResultError("submission_unknown"), nil
	}))}
	wrapped := wrapToolsWithInvocationRetry(base, func(string) ToolInvocationPolicy { return ToolInvocationPolicy{MaxAttempts: 3} })
	result, err := wrapped[0].Handler(context.Background(), &tools.Request{})
	if err != nil || result == nil || !result.IsError || calls != 1 {
		t.Fatalf("unsafe failure was retried: calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestInvocationRetryDoesNotStartAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	base := []*tools.Tool{tools.NewTool("bash", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
		calls++
		cancel()
		return tools.NewToolResultRetryableError("transient", "test"), nil
	}))}
	wrapped := wrapToolsWithInvocationRetry(base, func(string) ToolInvocationPolicy { return ToolInvocationPolicy{MaxAttempts: 2} })
	_, err := wrapped[0].Handler(ctx, &tools.Request{})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled invocation started another attempt: calls=%d err=%v", calls, err)
	}
}

func TestRetryWithInnermostTraceProducesEventPerAttempt(t *testing.T) {
	calls := 0
	var events []ToolTraceEvent
	base := []*tools.Tool{tools.NewTool("flaky", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
		calls++
		if calls <= 2 {
			return tools.NewToolResultRetryableError("transient failure", "test flakiness"), nil
		}
		return tools.NewToolResultText("ok"), nil
	}))}

	// Trace is innermost (closest to the real handler, so each retry attempt
	// is traced); retry sits outside and supplies the invocation evidence.
	wrapped := wrapToolsWithInvocationRetry(
		wrapToolsWithTrace(base, func(event ToolTraceEvent) { events = append(events, event) }),
		func(string) ToolInvocationPolicy { return ToolInvocationPolicy{MaxAttempts: 3} },
	)

	result, err := wrapped[0].Handler(context.Background(), &tools.Request{})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("expected eventual success, got result=%+v err=%v", result, err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}

	var starts, ends, errorEvents int
	attempts := map[int]int{}
	for _, event := range events {
		switch event.Kind {
		case ToolTraceStart:
			starts++
		case ToolTraceEnd:
			ends++
		case ToolTraceError:
			errorEvents++
		}
		attempts[event.Attempt]++
	}
	if starts != 3 || errorEvents != 2 || ends != 1 {
		t.Fatalf("expected 3 start / 2 error / 1 end events, got %d/%d/%d: %+v", starts, errorEvents, ends, events)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if attempts[attempt] != 2 { // one start + one terminal event per attempt
			t.Fatalf("attempt %d seen %d times, expected 2", attempt, attempts[attempt])
		}
	}
}

func TestRetryGivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	var events []ToolTraceEvent
	base := []*tools.Tool{tools.NewTool("always-fails", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
		calls++
		return tools.NewToolResultRetryableError("still broken", "test"), nil
	}))}
	wrapped := wrapToolsWithInvocationRetry(
		wrapToolsWithTrace(base, func(event ToolTraceEvent) { events = append(events, event) }),
		func(string) ToolInvocationPolicy {
			return ToolInvocationPolicy{MaxAttempts: 2, Backoff: time.Millisecond}
		},
	)

	result, err := wrapped[0].Handler(context.Background(), &tools.Request{})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected recorded failure result, got result=%+v err=%v", result, err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts before giving up, got %d", calls)
	}
	var terminalErrors []ToolTraceEvent
	for _, event := range events {
		if event.Kind == ToolTraceError {
			terminalErrors = append(terminalErrors, event)
		}
	}
	if len(terminalErrors) != 2 || terminalErrors[1].Attempt != 2 {
		t.Fatalf("expected failure recorded at attempt 2, got %+v", events)
	}
}

func TestTraceArgumentsRedactedAndCapped(t *testing.T) {
	var events []ToolTraceEvent
	base := []*tools.Tool{tools.NewTool("bash", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
		return tools.NewToolResultText("done"), nil
	}))}
	wrapped := wrapToolsWithTrace(base, func(event ToolTraceEvent) { events = append(events, event) })

	_, err := wrapped[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{
		"command":    "export API_KEY=sk-super-secret && curl -H 'Authorization: Bearer tok123' example.com",
		"api_key":    "sk-super-secret",
		"retryDelay": "1s",
	}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	var args map[string]interface{}
	for _, event := range events {
		if event.Kind == ToolTraceStart {
			args = event.Arguments
		}
	}
	if args == nil {
		t.Fatal("expected a start event with arguments")
	}
	if masked, ok := args["api_key"].(string); !ok || masked != maskedToolArgumentValue {
		t.Fatalf("expected api_key to be masked, got %#v", args["api_key"])
	}
	command, _ := args["command"].(string)
	if strings.Contains(command, "sk-super-secret") || strings.Contains(command, "tok123") {
		t.Fatalf("expected embedded secrets to be masked, got %q", command)
	}
	if !strings.Contains(command, "API_KEY="+maskedToolArgumentValue) {
		t.Fatalf("expected masked assignment retained, got %q", command)
	}
	if args["retryDelay"] != "1s" {
		t.Fatalf("expected non-sensitive argument untouched, got %#v", args["retryDelay"])
	}

	// Oversized argument values are capped like results.
	events = nil
	bigTool := []*tools.Tool{tools.NewTool("writer", tools.WithToolHandler(func(context.Context, *tools.Request) (*tools.Result, error) {
		return tools.NewToolResultText("done"), nil
	}))}
	wrapped = wrapToolsWithTrace(bigTool, func(event ToolTraceEvent) { events = append(events, event) })
	if _, err := wrapped[0].Handler(context.Background(), &tools.Request{Arguments: map[string]interface{}{
		"content": strings.Repeat("a", maxToolTraceResultLen+500),
	}}); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	for _, event := range events {
		if event.Kind != ToolTraceStart {
			continue
		}
		content, _ := event.Arguments["content"].(string)
		if len(content) > maxToolTraceResultLen+len("...[truncated]") {
			t.Fatalf("expected capped argument, got %d bytes", len(content))
		}
	}
}

func TestTruncateStringBacksOffToRuneBoundary(t *testing.T) {
	s := strings.Repeat("é", 100)
	got := truncateString(s, 101)
	if !strings.HasSuffix(got, "...[truncated]") {
		t.Fatalf("expected truncated suffix, got %q", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("truncated string splits a rune: %q", got)
		}
	}
	if got := truncateString("short", 10); got != "short" {
		t.Fatalf("expected short string unchanged, got %q", got)
	}
}

func TestToolRetryBackoffExponentialCappedWithJitter(t *testing.T) {
	base := 500 * time.Millisecond
	seen := map[time.Duration]bool{}
	for attempt := 1; attempt <= 10; attempt++ {
		delay := toolRetryBackoff(attempt, base)
		if delay <= 0 {
			t.Fatalf("attempt %d: expected positive backoff, got %v", attempt, delay)
		}
		maxDelay := time.Duration(1<<uint(attempt-1)) * base
		if maxDelay > maxToolRetryBackoff {
			maxDelay = maxToolRetryBackoff
		}
		if delay < maxDelay/2 || delay > maxDelay {
			t.Fatalf("attempt %d: backoff %v outside [%v, %v]", attempt, delay, maxDelay/2, maxDelay)
		}
		seen[delay] = true
	}
	if len(seen) < 2 {
		t.Fatal("expected jitter to produce varying delays")
	}
	if delay := toolRetryBackoff(1, 0); delay != 0 {
		t.Fatalf("expected zero backoff for zero base, got %v", delay)
	}
}

func TestDefaultToolInvocationPolicy(t *testing.T) {
	policy := defaultToolInvocationPolicy("bash")
	if policy.MaxAttempts != defaultToolRetryMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", defaultToolRetryMaxAttempts, policy.MaxAttempts)
	}
	if policy.Backoff != defaultToolRetryBaseBackoff {
		t.Fatalf("expected base backoff %v, got %v", defaultToolRetryBaseBackoff, policy.Backoff)
	}
}
