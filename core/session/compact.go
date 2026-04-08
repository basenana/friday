package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
)

const (
	compactTailMessages = 6
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
	history := s.GetHistory()
	if len(history) == 0 {
		return nil
	}

	ctxState := s.EnsureContextState()
	if s.llm == nil && len(history) <= compactTailMessages {
		return nil
	}

	summary, err := SummarizeToCaseFile(ctx, s.llm, ctxState.CaseFile, compactableMessages(history, compactTailMessages))
	if err != nil {
		return err
	}
	if summary.IsZero() {
		return fmt.Errorf("summary is empty")
	}

	ctxState.CaseFile = MergeCaseFiles(ctxState.CaseFile, summary)
	ctxState.LastCompactionAt = nowOr(history[len(history)-1].Time)
	ctxState.LastCompactionTokens = tokenCount(history)

	compacted := BuildCompactedHistory(history, ctxState.CaseFile, compactTailMessages)
	return s.ReplaceHistory(compacted...)
}

func BuildCompactedHistory(history []types.Message, cf CaseFile, tail int) []types.Message {
	if len(history) == 0 {
		return nil
	}

	filtered := filterCompactHistory(history)
	keep := truncateToLastN(filtered, tail)
	result := make([]types.Message, 0, len(keep)+2)
	if len(filtered) > 0 {
		// Always preserve the very first message (typically the initial user request)
		// so the agent retains the original task objective even after compaction.
		result = append(result, filtered[0])
		// When filtered is short enough to fit within tail, keep already contains
		// filtered[0], so skip it to avoid duplication.
		if len(filtered) <= tail && len(keep) > 0 {
			keep = keep[1:]
		}
	}
	result = append(result, cf.ToMessage())
	result = append(result, keep...)
	return result
}

func compactableMessages(history []types.Message, tail int) []types.Message {
	filtered := filterCompactHistory(history)
	if len(filtered) <= tail {
		return filtered
	}
	return filtered[:len(filtered)-tail]
}

func filterCompactHistory(history []types.Message) []types.Message {
	result := make([]types.Message, 0, len(history))
	for _, msg := range history {
		if IsSyntheticContextMessage(msg) {
			continue
		}
		switch msg.Role {
		case types.RoleUser, types.RoleAgent, types.RoleAssistant, types.RoleTool:
			result = append(result, msg)
		}
	}
	return result
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

func compactPrompt(history []types.Message, existing CaseFile) string {
	existingJSON := "{}"
	if !existing.IsZero() {
		if raw, err := json.Marshal(existing); err == nil {
			existingJSON = string(raw)
		}
	}

	return fmt.Sprintf(`You are compressing an autonomous agent conversation into a durable case file.
Return a single JSON object and nothing else.

The JSON schema is:
{
  "task_objective": "string",
  "user_constraints": ["string"],
  "architecture_decisions": ["string"],
  "current_status": "string",
  "pending_work": ["string"],
  "recent_requests": ["string"],
  "recent_files": ["string"],
  "important_commands_or_tools": ["string"],
  "known_risks": ["string"],
  "timeline_highlights": ["string"]
}

Rules:
- Return every field in the schema. Use "" for unknown strings and [] for unknown lists.
- Preserve concrete next steps, decisions, file paths, and unresolved work.
- Prefer short list items over paragraphs.
- Do not include markdown fences.
- Keep recent_files to the most relevant 5 entries.
- Keep recent_requests to the most relevant 3 entries.

Existing case file JSON:
%s

Conversation history:
%s`, existingJSON, formatHistoryForPrompt(history))
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
