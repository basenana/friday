package providers

import (
	"sync"
	"testing"

	"github.com/basenana/friday/core/types"
)

func TestNewPromptRequestBuildsSingleUserMessage(t *testing.T) {
	req := NewPromptRequest("summarize the latest tool outputs")

	if got := req.SystemPrompt(); got != "" {
		t.Fatalf("expected prompt-only request to have no system prompt, got %q", got)
	}

	history := req.History()
	if len(history) != 1 {
		t.Fatalf("expected one history message, got %d", len(history))
	}
	if history[0].Role != types.RoleUser {
		t.Fatalf("expected prompt-only request history role=user, got %q", history[0].Role)
	}
	if history[0].Content != "summarize the latest tool outputs" {
		t.Fatalf("expected prompt content to be preserved, got %q", history[0].Content)
	}
}

func TestNewRequestSkipsEmptySystemPrompt(t *testing.T) {
	req := NewRequest("", types.Message{Role: types.RoleUser, Content: "hello"})

	if got := req.SystemPrompt(); got != "" {
		t.Fatalf("expected empty system prompt to stay empty, got %q", got)
	}

	messages := req.Messages()
	if len(messages) != 1 {
		t.Fatalf("expected no synthetic blank system message, got %#v", messages)
	}
	if messages[0].Role != types.RoleUser || messages[0].Content != "hello" {
		t.Fatalf("expected original user message to be preserved, got %#v", messages[0])
	}
}

// TestCommonResponseTokensConcurrentAccess exercises the race fixed by
// AddTokens/SetTokens: a streaming goroutine accumulates usage while readers
// call Tokens() concurrently. Run with -race.
func TestCommonResponseTokensConcurrentAccess(t *testing.T) {
	resp := NewCommonResponse()
	defer close(resp.Stream)
	defer close(resp.Err)

	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		for i := 0; i < 500; i++ {
			resp.AddTokens(Tokens{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3})
		}
	}()
	go func() {
		defer writers.Done()
		for i := 0; i < 500; i++ {
			resp.AddTokens(Tokens{CompletionTokens: 1, TotalTokens: 1})
		}
	}()

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 500; i++ {
				if got := resp.Tokens(); got.PromptTokens < 0 || got.CompletionTokens < 0 {
					t.Errorf("unexpected negative token snapshot: %#v", got)
					return
				}
			}
		}()
	}

	readers.Wait()
	writers.Wait()

	got := resp.Tokens()
	if got.PromptTokens != 500 || got.CompletionTokens != 1500 || got.TotalTokens != 2000 {
		t.Fatalf("unexpected accumulated usage: %#v", got)
	}
}
