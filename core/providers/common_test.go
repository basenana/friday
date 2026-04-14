package providers

import (
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
