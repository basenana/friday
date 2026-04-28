package agents

import (
	"fmt"
	"strings"
	"testing"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

func TestTruncateToolResult(t *testing.T) {
	t.Run("fallback to default when PromptBudget not initialized", func(t *testing.T) {
		sess := session.New("sess-trunc-default", &fakeLLMClient{})
		long := strings.Repeat("a", defaultMaxToolResultChars+100)

		result := truncateToolResult(sess, long)
		if !strings.HasSuffix(result, fmt.Sprintf("showing %d of %d chars]", defaultMaxToolResultChars, defaultMaxToolResultChars+100)) {
			t.Fatalf("expected truncation suffix, got tail: %q", result[len(result)-80:])
		}
		// Truncated body should be exactly defaultMaxToolResultChars runes
		// before the suffix line
		lines := strings.SplitN(result, "\n", 2)
		if len(lines) != 2 {
			t.Fatalf("expected body + suffix line, got %d parts", len(lines))
		}
		if len([]rune(lines[0])) != defaultMaxToolResultChars {
			t.Fatalf("expected body length %d, got %d", defaultMaxToolResultChars, len([]rune(lines[0])))
		}
	})

	t.Run("truncate based on remaining budget", func(t *testing.T) {
		sess := session.New("sess-trunc-budget", &fakeLLMClient{})
		st := sess.EnsureContextState()
		st.PromptBudget.ContextWindow = 1000
		// Session tokens will be ~0 for empty history, so remaining ~ 1000
		// char limit = 1000 * 2 = 2000
		charLimit := 1000 * charsPerToken

		long := strings.Repeat("b", charLimit+500)
		result := truncateToolResult(sess, long)

		if !strings.HasSuffix(result, fmt.Sprintf("showing %d of %d chars]", charLimit, charLimit+500)) {
			t.Fatalf("expected truncation suffix, got tail: %q", result[len(result)-80:])
		}
		lines := strings.SplitN(result, "\n", 2)
		if len([]rune(lines[0])) != charLimit {
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
		extra := 200
		long := strings.Repeat("c", defaultMaxToolResultChars+extra)

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

		long := strings.Repeat("d", minToolResultChars+500)
		result := truncateToolResult(sess, long)

		if !strings.Contains(result, fmt.Sprintf("showing %d of %d chars]", minToolResultChars, minToolResultChars+500)) {
			t.Fatalf("expected minToolResultChars limit, got tail: %q", result[len(result)-80:])
		}
		lines := strings.SplitN(result, "\n", 2)
		if len([]rune(lines[0])) != minToolResultChars {
			t.Fatalf("expected body length %d, got %d", minToolResultChars, len([]rune(lines[0])))
		}
	})
}
