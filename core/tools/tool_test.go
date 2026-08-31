package tools

import (
	"context"
	"strings"
	"testing"
)

func TestValidateRequiredArguments(t *testing.T) {
	tool := NewTool("demo_search",
		WithString("pattern", Required(), Description("Text pattern to search for.")),
		WithString("path", Description("Directory path to search in.")),
	)

	t.Run("all required supplied", func(t *testing.T) {
		if msg := tool.ValidateRequiredArguments(map[string]interface{}{"pattern": "hello"}); msg != "" {
			t.Fatalf("expected no message, got %q", msg)
		}
	})

	t.Run("missing required parameter includes name and description", func(t *testing.T) {
		msg := tool.ValidateRequiredArguments(map[string]interface{}{})
		if msg == "" {
			t.Fatal("expected validation message")
		}
		if !strings.Contains(msg, "'pattern'") {
			t.Fatalf("expected parameter name in message, got %q", msg)
		}
		if !strings.Contains(msg, "Text pattern to search for.") {
			t.Fatalf("expected parameter description in message, got %q", msg)
		}
		if !strings.Contains(msg, "retry the tool call") {
			t.Fatalf("expected retry guidance in message, got %q", msg)
		}
	})

	t.Run("empty string counts as supplied", func(t *testing.T) {
		if msg := tool.ValidateRequiredArguments(map[string]interface{}{"pattern": ""}); msg != "" {
			t.Fatalf("expected empty string to be treated as supplied, got %q", msg)
		}
	})

	t.Run("optional parameter absent is fine", func(t *testing.T) {
		if msg := tool.ValidateRequiredArguments(map[string]interface{}{"pattern": "x"}); msg != "" {
			t.Fatalf("expected no message, got %q", msg)
		}
	})
}

func TestValidateRequiredArgumentsNoDescription(t *testing.T) {
	tool := NewTool("demo",
		WithString("name", Required()),
		WithToolHandler(func(ctx context.Context, req *Request) (*Result, error) {
			return NewToolResultText("ok"), nil
		}),
	)
	msg := tool.ValidateRequiredArguments(map[string]interface{}{})
	if !strings.Contains(msg, "'name'") {
		t.Fatalf("expected parameter name in message, got %q", msg)
	}
}
