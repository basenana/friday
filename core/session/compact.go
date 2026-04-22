package session

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
)

var (
	filePathPattern = regexp.MustCompile("(?i)(?:^|[\\s(\"'])(?:((?:\\.{0,2}/|/)?[A-Za-z0-9_.~\\-/]+?\\.(?:go|mod|sum|ts|tsx|js|jsx|json|yaml|yml|toml|md|txt|py|rs|java|c|cc|cpp|h|hpp|sh|sql|html|css|xml)))(?:$|[\\s)\"',:;])")
)

const (
	compactTailMessages         = 6
	compactFallbackKeepMessages = 20
)

var (
	// CompactThreshold is the token limit that triggers compaction.
	// Can be overridden via FRIDAY_COMPACT_THRESHOLD environment variable.
	CompactThreshold int64 = 128 * 1000 // 128K tokens default
)

func init() {
	if ctStr := os.Getenv("FRIDAY_COMPACT_THRESHOLD"); ctStr != "" {
		ct, _ := strconv.ParseInt(ctStr, 10, 64)
		if ct > 1000 {
			CompactThreshold = ct
		}
	}
}

func (s *Session) autoCompactHistory(ctx context.Context, req providers.Request) error {
	if s.compactThreshold < 0 {
		return nil
	}

	beforeTokens := tokenCount(s.GetHistory())
	if beforeTokens < s.compactThreshold {
		return nil
	}

	if err := s.CompactHistory(ctx); err != nil {
		return err
	}

	req.SetHistory(s.GetHistory())
	return nil
}

func (s *Session) CompactHistory(ctx context.Context) error {
	ctx, span := tracing.Start(ctx, "session.compact",
		tracing.WithAttributes(
			tracing.String("session.id", s.ID),
			tracing.String("session.root_id", s.Root.ID),
			tracing.IntVal("history_len", len(s.GetHistory())),
		),
	)
	defer span.End()

	history := s.GetHistory()
	if len(history) == 0 {
		return nil
	}

	s.PublishEvent(types.Event{
		Type: types.EventCompactStart,
		Data: map[string]string{
			"history_len": strconv.Itoa(len(history)),
			"trigger":     "threshold",
		},
	})

	ctxState := s.EnsureContextState()

	// Reset token checkpoint since history will be replaced.
	ctxState.TokenCheckpoint = TokenCheckpoint{}

	// Without an LLM, fallback to keeping the last N messages instead of guessing.
	if s.llm == nil {
		compacted := truncateToLastN(history, compactFallbackKeepMessages)
		if err := s.ReplaceHistory(compacted...); err != nil {
			return err
		}
		ctxState = s.EnsureContextState()
		ctxState.LastCompactionAt = nowOr(history[len(history)-1].Time)
		ctxState.LastCompactionTokens = tokenCount(history)
		s.PublishEvent(types.Event{
			Type: types.EventCompactFinish,
			Data: map[string]string{"method": "truncate"},
		})
		return nil
	}

	summary, err := SummarizeToCompactSummary(ctx, s.llm, history)
	if err != nil {
		return err
	}
	if strings.TrimSpace(summary) == "" {
		// LLM returned empty summary; fallback to keeping the last N messages.
		compacted := truncateToLastN(history, compactFallbackKeepMessages)
		if err := s.ReplaceHistory(compacted...); err != nil {
			return err
		}
		ctxState = s.EnsureContextState()
		ctxState.LastCompactionAt = nowOr(history[len(history)-1].Time)
		ctxState.LastCompactionTokens = tokenCount(history)
		s.PublishEvent(types.Event{
			Type: types.EventCompactFinish,
			Data: map[string]string{"method": "truncate"},
		})
		return nil
	}

	compacted := BuildCompactSummaryHistory(history, summary)
	if err := s.ReplaceHistory(compacted...); err != nil {
		return err
	}
	ctxState = s.EnsureContextState()
	ctxState.LastCompactionAt = nowOr(history[len(history)-1].Time)
	ctxState.LastCompactionTokens = tokenCount(history)
	s.PublishEvent(types.Event{
		Type: types.EventCompactFinish,
		Data: map[string]string{"method": "summary"},
	})
	return nil
}

// SummarizeToCompactSummary produces a natural-language summary of the conversation
// history suitable for the Claude-style compact format.
func SummarizeToCompactSummary(ctx context.Context, llm providers.Client, history []types.Message) (string, error) {
	ctx, span := tracing.Start(ctx, "session.summarize",
		tracing.WithAttributes(tracing.IntVal("history_len", len(history))),
	)
	defer span.End()

	if len(history) == 0 {
		return "", nil
	}

	if llm == nil {
		return "", nil
	}

	req := providers.NewPromptRequest(compactSummaryPrompt(history))
	resp := llm.Completion(ctx, req)
	msgCh := resp.Message()

	var raw strings.Builder
Loop:
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err := <-resp.Error():
			if err != nil {
				return "", err
			}
		case delta, ok := <-msgCh:
			if !ok {
				break Loop
			}
			if delta.Content != "" {
				raw.WriteString(delta.Content)
			}
		}
	}

	result := strings.TrimSpace(raw.String())
	if result != "" {
		return result, nil
	}
	// Don't fall back to heuristic if the context was cancelled
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", nil
}

// BuildCompactSummaryHistory replaces the old history with a compact summary
// message followed by the tail of recent messages.
func BuildCompactSummaryHistory(history []types.Message, summary string) []types.Message {
	if len(history) == 0 {
		return nil
	}

	keep := truncateToLastN(history, compactTailMessages)
	result := make([]types.Message, 0, len(keep)+1)
	result = append(result, BuildCompactSummaryMessage(summary))
	result = append(result, keep...)
	return result
}

// FallbackCompactHistory returns the last n messages of history.
// Used when LLM summarization is unavailable.
func FallbackCompactHistory(history []types.Message, n int) []types.Message {
	return truncateToLastN(history, n)
}

// CompactFallbackKeepMessages returns the number of messages to keep when
// LLM summarization is unavailable.
func CompactFallbackKeepMessages() int {
	return compactFallbackKeepMessages
}

// BuildCompactSummaryMessage wraps a summary string into an agent message
// suitable for injecting into history.
func BuildCompactSummaryMessage(summary string) types.Message {
	return types.Message{
		Role:    types.RoleAgent,
		Content: summaryPrefix + summary,
	}
}

func compactSummaryPrompt(history []types.Message) string {
	return fmt.Sprintf(`You are summarizing a conversation between a user and an AI assistant.

Produce a concise but comprehensive summary that captures:
1. The user's original goal or task
2. Key decisions and progress made
3. Important files, paths, or artifacts referenced
4. Unresolved issues or pending work
5. Any constraints or requirements the user specified

Rules:
- Write in plain text, not JSON or markdown.
- Be concise but preserve concrete details (file paths, error messages, tool names).
- Focus on information that would be needed to continue the conversation effectively.
- Do not include greetings or filler text.

Conversation history:
%s`, formatHistoryForPrompt(history))
}

// truncateToLastN keeps only the last n messages.
func truncateToLastN(history []types.Message, n int) []types.Message {
	if len(history) <= n {
		return append([]types.Message(nil), history...)
	}
	return append([]types.Message(nil), history[len(history)-n:]...)
}

// tokenCount calculates total tokens for a history.
func tokenCount(history []types.Message) int64 {
	var total int64
	for _, msg := range history {
		total += msg.FuzzyTokens()
	}
	return total
}

// formatHistoryForPrompt formats history for the summarization prompt.
func formatHistoryForPrompt(history []types.Message) string {
	var lines []string
	for i, msg := range history {
		switch msg.Role {
		case types.RoleUser:
			lines = append(lines, fmt.Sprintf("[User %d] %s", i+1, msg.Content))
		case types.RoleAgent:
			lines = append(lines, fmt.Sprintf("[Agent %d] %s", i+1, msg.Content))
		case types.RoleAssistant:
			if msg.Content != "" {
				lines = append(lines, fmt.Sprintf("[Assistant %d] %s", i+1, msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				lines = append(lines, fmt.Sprintf("[ToolCall %d] %s(%s)", i+1, tc.Name, tc.Arguments))
			}
		case types.RoleTool:
			if msg.ToolResult != nil {
				lines = append(lines, fmt.Sprintf("[ToolResult %d] %s", i+1, msg.ToolResult.Content))
			}
		}
	}
	return strings.Join(filterEmpty(lines, 3), "\n")
}

// filterEmpty filters out strings shorter than minLen.
func filterEmpty(lines []string, minLen int) []string {
	var result []string
	for _, line := range lines {
		if len(line) > minLen {
			result = append(result, line)
		}
	}
	return result
}

const summaryPrefix = `Several lengthy dialogues have already taken place. The following is a condensed summary of the progress of these historical dialogues:
`

// RebuildHistoryWithAbstract replaces old history with a summary.
func RebuildHistoryWithAbstract(history []types.Message, abstract string) []types.Message {
	if abstract == "" || len(history) == 0 {
		return history
	}

	effectiveHistory := make([]types.Message, 0, len(history))
	for _, msg := range history {
		if msg.Role == types.RoleUser || msg.Role == types.RoleAgent || msg.Role == types.RoleAssistant {
			effectiveHistory = append(effectiveHistory, msg)
		}
	}

	abstractMessage := types.Message{Role: types.RoleAgent, Content: summaryPrefix + abstract}
	if len(effectiveHistory) == 0 {
		return []types.Message{abstractMessage}
	}

	var (
		cutAt      = len(effectiveHistory) - 3
		newHistory []types.Message
	)

	if cutAt < 0 {
		cutAt = 0
	}

	if cutAt == 0 {
		effectiveHistory = append(effectiveHistory, abstractMessage)
		return effectiveHistory
	}

	keep := effectiveHistory[cutAt:]
	newHistory = append(newHistory, effectiveHistory[0])
	newHistory = append(newHistory, abstractMessage)
	newHistory = append(newHistory, keep...)
	return newHistory
}

func nowOr(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now()
	}
	return ts
}

func extractFileRefsFromHistory(history []types.Message) []FileRef {
	var refs []FileRef
	for _, msg := range history {
		refs = append(refs, ExtractFileRefs(msg.GetContent(), string(msg.Role), msg.Time)...)
		if msg.IsToolCall() {
			for _, call := range msg.ToolCalls {
				refs = append(refs, ExtractFileRefs(call.Arguments, call.Name, msg.Time)...)
			}
		}
	}
	return dedupeFileRefs(refs, defaultRecentFileLimit)
}

// ExtractFileRefs extracts file path references from text.
func ExtractFileRefs(text, source string, seenAt time.Time) []FileRef {
	matches := filePathPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]FileRef, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		path = strings.Trim(path, `"'`)
		if path == "" {
			continue
		}
		result = append(result, FileRef{Path: path, Source: source, SeenAt: seenAt})
	}
	return dedupeFileRefs(result, defaultRecentFileLimit)
}

func dedupeFileRefs(refs []FileRef, limit int) []FileRef {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]FileRef)
	order := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Path == "" {
			continue
		}
		seen[ref.Path] = ref
		order = append(order, ref.Path)
	}

	slices.Reverse(order)
	var result []FileRef
	added := make(map[string]struct{})
	for _, path := range order {
		if _, ok := added[path]; ok {
			continue
		}
		result = append(result, seen[path])
		added[path] = struct{}{}
		if len(result) >= limit {
			break
		}
	}
	slices.Reverse(result)
	return result
}
