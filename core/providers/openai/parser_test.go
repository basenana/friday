package openai

import (
	"errors"
	"testing"

	"github.com/basenana/friday/core/providers"
	openaisdk "github.com/openai/openai-go"
)

func writeThinkingContent(t *testing.T, resp *response, chunks ...string) []providers.Delta {
	t.Helper()
	var deltas []providers.Delta
	for _, chunk := range chunks {
		for _, delta := range resp.thinking.write(chunk) {
			resp.emit(delta)
			deltas = append(deltas, delta)
		}
	}
	return deltas
}

// Reasoning inside a <think> section must be emitted incrementally as it
// arrives instead of being buffered until the closing tag.
func TestThinkingParserEmitsReasoningIncrementally(t *testing.T) {
	resp := newResponse(nil)
	deltas := writeThinkingContent(t, resp, "<think>hello ", "world ", "from ", "the model")
	resp.close()

	var reasoning, content string
	for _, delta := range deltas {
		reasoning += delta.Reasoning
		content += delta.Content
	}
	if reasoning != "hello world from the model" {
		t.Fatalf("expected incremental reasoning, got %q", reasoning)
	}
	if content != "" {
		t.Fatalf("expected no content while inside think section, got %q", content)
	}
}

// A closing </think> tag split across chunks must be detected while the
// preceding reasoning text is still streamed through.
func TestThinkingParserHandlesSplitClosingTag(t *testing.T) {
	resp := newResponse(nil)
	deltas := writeThinkingContent(t, resp, "<think>reasoning part</thi", "nk>answer text")
	resp.close()

	var reasoning, content string
	for _, delta := range deltas {
		reasoning += delta.Reasoning
		content += delta.Content
	}
	if reasoning != "reasoning part" {
		t.Fatalf("expected reasoning to stop at closing tag, got %q", reasoning)
	}
	if content != "answer text" {
		t.Fatalf("expected content after closing tag, got %q", content)
	}
}

// After a stream error the deferred close() must not re-emit buffered
// reasoning as Content.
func TestThinkingParserFailedStreamEmitsNoContent(t *testing.T) {
	resp := newResponse(nil)
	deltas := writeThinkingContent(t, resp, "<think>partial reasoning that never closes")
	resp.fail(errors.New("stream error"))
	resp.close()

	var reasoning, content string
	for _, delta := range deltas {
		reasoning += delta.Reasoning
		content += delta.Content
	}
	if content != "" {
		t.Fatalf("expected no content deltas after stream failure, got %q", content)
	}
	if reasoning != "partial reasoning that never closes" {
		t.Fatalf("expected buffered reasoning to have been streamed, got %q", reasoning)
	}
}

func TestIsThirdPartyHost(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"", false},
		{"https://api.openai.com/v1", false},
		{"https://api.openai.com", false},
		{"https://eu.api.openai.com/v1", false},
		{"https://api.minimax.chat/v1", true},
		{"https://open.bigmodel.cn/api/paas/v4", true},
		{"http://localhost:8000/v1", true},
	}
	for _, tc := range cases {
		if got := isThirdPartyHost(tc.baseURL, "api.openai.com"); got != tc.want {
			t.Fatalf("isThirdPartyHost(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestClientMaxOutputTokens(t *testing.T) {
	cli := &client{model: Model{Name: "gpt-test", MaxTokens: 8192}}
	if got := cli.MaxOutputTokens(); got != 8192 {
		t.Fatalf("expected 8192, got %d", got)
	}
	cli = &client{model: Model{Name: "gpt-test"}}
	if got := cli.MaxOutputTokens(); got != 0 {
		t.Fatalf("expected 0 for unset MaxTokens, got %d", got)
	}
}

// The usage snapshot must be observable via Tokens() while the stream
// goroutine accumulates usage deltas. Run with -race.
func TestResponseUpdateUsageConcurrentWithTokensReads(t *testing.T) {
	resp := newResponse(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			resp.updateUsage(openaisdk.CompletionUsage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			})
		}
	}()
	for i := 0; i < 500; i++ {
		if got := resp.Tokens(); got.PromptTokens < 0 {
			t.Fatalf("unexpected negative snapshot: %#v", got)
		}
	}
	<-done

	got := resp.Tokens()
	if got.PromptTokens != 500 || got.CompletionTokens != 1000 || got.TotalTokens != 1500 {
		t.Fatalf("unexpected accumulated usage: %#v", got)
	}
}
