package common

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	openaiapi "github.com/openai/openai-go"
)

func TestIsRetriableErrorTypedStatusCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"anthropic 429", &anthropicapi.Error{StatusCode: 429}, true},
		{"anthropic 408", &anthropicapi.Error{StatusCode: 408}, true},
		{"anthropic 409", &anthropicapi.Error{StatusCode: 409}, true},
		{"anthropic 500", &anthropicapi.Error{StatusCode: 500}, true},
		{"anthropic 503", &anthropicapi.Error{StatusCode: 503}, true},
		{"anthropic 529", &anthropicapi.Error{StatusCode: 529}, true},
		{"anthropic 400", &anthropicapi.Error{StatusCode: 400}, false},
		{"anthropic 404", &anthropicapi.Error{StatusCode: 404}, false},
		{"openai 429", &openaiapi.Error{StatusCode: 429}, true},
		{"openai 502", &openaiapi.Error{StatusCode: 502}, true},
		{"openai 401", &openaiapi.Error{StatusCode: 401}, false},
		{"wrapped typed 429", fmt.Errorf("stream failed: %w", &anthropicapi.Error{StatusCode: 429}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetriableError(tc.err); got != tc.want {
				t.Fatalf("IsRetriableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsRetriableErrorUntypedMessages(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"too many requests", "429 too many requests", true},
		{"rate limit exceeded", "rate limit exceeded, retry later", true},
		{"rate_limit_error", "rate_limit_error", true},
		{"overloaded", "Overloaded Error", true},
		{"service unavailable", "503 Service Unavailable", true},
		{"bad gateway", "502 Bad Gateway", true},
		{"explicit status phrase", "unexpected response: status 503", true},
		{"request id containing digits", `request id "req_1750293503abc" not found`, false},
		{"model name containing digits", "model gpt-4-turbo-503-preview not found", false},
		{"unrelated 500 in id", "conversation 500abc failed with invalid request", false},
		{"connection reset", "connection reset by peer", true},
		{"context canceled", "context canceled", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetriableError(errors.New(tc.msg)); got != tc.want {
				t.Fatalf("IsRetriableError(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func TestRetryBackoffDelayUsesBoundedExponentialJitter(t *testing.T) {
	for attempt, bounds := range map[int][2]time.Duration{
		1: {400 * time.Millisecond, 600 * time.Millisecond},
		2: {800 * time.Millisecond, 1200 * time.Millisecond},
	} {
		got := RetryBackoffDelay(attempt)
		if got < bounds[0] || got > bounds[1] {
			t.Fatalf("RetryBackoffDelay(%d) = %v, want within %v", attempt, got, bounds)
		}
	}
}

func TestRetryAfterParsesAndCapsServerDelay(t *testing.T) {
	err := &openaiapi.Error{Response: &http.Response{Header: http.Header{"Retry-After": []string{"60"}}}}
	if got, ok := RetryAfter(err, time.Now()); !ok || got != 30*time.Second {
		t.Fatalf("RetryAfter() = %v, %v; want 30s, true", got, ok)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 80); got != "short" {
		t.Fatalf("Truncate kept string unchanged, got %q", got)
	}
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'a'
	}
	if got := Truncate(string(long), 80); got != string(long[:80])+"..." {
		t.Fatalf("Truncate truncated with ellipsis, got %q", got)
	}
}
