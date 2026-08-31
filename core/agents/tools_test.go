package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

func TestTruncateToolResult(t *testing.T) {
	t.Run("fallback to default when PromptBudget not initialized", func(t *testing.T) {
		sess := session.New("sess-trunc-default", &fakeLLMClient{})
		long := strings.Repeat("a", int(defaultMaxToolResultChars)+100)

		result := truncateToolResult(sess, long)
		if !strings.HasSuffix(result, fmt.Sprintf("showing %d of %d chars]", defaultMaxToolResultChars, int64(int(defaultMaxToolResultChars)+100))) {
			t.Fatalf("expected truncation suffix, got tail: %q", result[len(result)-80:])
		}
		// Truncated body should be exactly defaultMaxToolResultChars runes
		// before the suffix line
		lines := strings.SplitN(result, "\n", 2)
		if len(lines) != 2 {
			t.Fatalf("expected body + suffix line, got %d parts", len(lines))
		}
		if int64(len([]rune(lines[0]))) != defaultMaxToolResultChars {
			t.Fatalf("expected body length %d, got %d", defaultMaxToolResultChars, len([]rune(lines[0])))
		}
	})

	t.Run("truncate based on remaining budget", func(t *testing.T) {
		sess := session.New("sess-trunc-budget", &fakeLLMClient{})
		st := sess.EnsureContextState()
		st.PromptBudget.ContextWindow = 1000
		// Session tokens will be ~0 for empty history, so remaining ~ 1000
		// char limit = 1000 * 2 = 2000, then further reduced by reservedTokensForSummary (20000) → negative → minToolResultChars.
		// To exercise the budget path cleanly, set a much larger ContextWindow.
		st.PromptBudget.ContextWindow = 100000
		charLimit := int64(100000) * charsPerToken
		if charLimit > maxSingleToolResultChars {
			charLimit = maxSingleToolResultChars
		}

		long := strings.Repeat("b", int(charLimit)+500)
		result := truncateToolResult(sess, long)

		if !strings.HasSuffix(result, fmt.Sprintf("showing %d of %d chars]", charLimit, int64(int(charLimit)+500))) {
			t.Fatalf("expected truncation suffix, got tail: %q", result[len(result)-80:])
		}
		lines := strings.SplitN(result, "\n", 2)
		if int64(len([]rune(lines[0]))) != charLimit {
			t.Fatalf("expected body length %d, got %d", charLimit, len([]rune(lines[0])))
		}
	})

	t.Run("no truncation when content fits", func(t *testing.T) {
		sess := session.New("sess-trunc-fit", &fakeLLMClient{})
		short := "hello world"

		result := truncateToolResult(sess, short)
		if result != short {
			t.Fatalf("expected no truncation, got %q", result)
		}
	})

	t.Run("truncation message includes lengths", func(t *testing.T) {
		sess := session.New("sess-trunc-msg", &fakeLLMClient{})
		extra := int64(200)
		long := strings.Repeat("c", int(defaultMaxToolResultChars+extra))

		result := truncateToolResult(sess, long)
		expectedTotal := defaultMaxToolResultChars + extra
		expect := fmt.Sprintf("showing %d of %d chars]", defaultMaxToolResultChars, expectedTotal)
		if !strings.Contains(result, expect) {
			t.Fatalf("expected %q in message, got tail: %q", expect, result[len(result)-100:])
		}
	})

	t.Run("over budget falls back to minToolResultChars", func(t *testing.T) {
		sess := session.New("sess-trunc-over", &fakeLLMClient{})
		st := sess.EnsureContextState()
		st.PromptBudget.ContextWindow = 100
		// Inject a large message so Tokens() exceeds ContextWindow
		sess.AppendMessage(&types.Message{
			Role:    types.RoleAssistant,
			Content: strings.Repeat("x", 2000),
		})

		long := strings.Repeat("d", int(minToolResultChars)+500)
		result := truncateToolResult(sess, long)

		if !strings.Contains(result, fmt.Sprintf("showing %d of %d chars]", minToolResultChars, int64(int(minToolResultChars)+500))) {
			t.Fatalf("expected minToolResultChars limit, got tail: %q", result[len(result)-80:])
		}
		lines := strings.SplitN(result, "\n", 2)
		if int64(len([]rune(lines[0]))) != minToolResultChars {
			t.Fatalf("expected body length %d, got %d", minToolResultChars, len([]rune(lines[0])))
		}
	})

	t.Run("hard cap clamps to maxSingleToolResultChars", func(t *testing.T) {
		sess := session.New("sess-trunc-cap", &fakeLLMClient{})
		st := sess.EnsureContextState()
		// Huge context window would normally produce a huge limit; the hard cap
		// should clamp it to maxSingleToolResultChars.
		st.PromptBudget.ContextWindow = 10_000_000

		long := strings.Repeat("z", int(maxSingleToolResultChars)+500)
		result := truncateToolResult(sess, long)

		if !strings.Contains(result, fmt.Sprintf("showing %d of %d chars]", maxSingleToolResultChars, int64(int(maxSingleToolResultChars)+500))) {
			t.Fatalf("expected maxSingleToolResultChars cap, got tail: %q", result[len(result)-100:])
		}
	})
}

func TestToolCallModelResultOmitsExecutionControlFields(t *testing.T) {
	exitCode := 127
	tool := tools.NewTool("exec_tool",
		tools.WithToolHandler(func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
			result := tools.NewToolResultRetryableError("command failed", "transient network error")
			result.ExitCode = &exitCode
			result.Cancelled = true
			return result, nil
		}),
	)

	sess := session.New("sess-tool-model-view", nil)
	msg, success, err := toolCall(context.Background(), sess,
		&ToolUse{GenID: "call-1", Name: "exec_tool", Arguments: `{}`}, tool)
	if err != nil {
		t.Fatalf("toolCall() error = %v", err)
	}
	if success {
		t.Fatal("expected success=false for an error result")
	}

	// The model-visible message must carry only the model-relevant fields.
	for _, leaked := range []string{"retryable", "retry_reason", "exit_code", "cancelled"} {
		if strings.Contains(msg, leaked) {
			t.Fatalf("model-visible tool result must not contain %q: %s", leaked, msg)
		}
	}
	if !strings.Contains(msg, `"is_error":true`) {
		t.Fatalf("expected is_error in model-visible tool result: %s", msg)
	}
	if !strings.Contains(msg, "command failed") {
		t.Fatalf("expected content text in model-visible tool result: %s", msg)
	}
}

func TestTruncateToolArgsSlicesRunesNotBytes(t *testing.T) {
	// 90 CJK characters: 270 bytes, so a byte-based slice would cut mid-rune.
	long := strings.Repeat("世", 90)
	got := truncateToolArgs(long)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateToolArgs() produced invalid UTF-8: %q", got)
	}
	runes := []rune(got)
	if len(runes) != 83 || !strings.HasSuffix(got, "...") || runes[0] != '世' || runes[79] != '世' {
		t.Fatalf("expected 80 runes plus ellipsis, got %d runes: %q", len(runes), got)
	}

	short := strings.Repeat("界", 80)
	if got := truncateToolArgs(short); got != short {
		t.Fatalf("expected no truncation, got %q", got)
	}
}
