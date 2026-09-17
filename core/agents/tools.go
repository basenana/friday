package agents

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/common"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
)

const (
	// defaultMaxToolResultChars is the fallback when PromptBudget is not yet initialized.
	defaultMaxToolResultChars int64 = 30000
	// minToolResultChars is used when the session is already over budget
	// (remaining tokens <= 0) to avoid injecting a full 30K default.
	minToolResultChars int64 = 4000
	// maxSingleToolResultChars is the absolute hard cap for any single tool result,
	// even when the context window still has budget. Keeps room for compact/summary.
	maxSingleToolResultChars int64 = 80000
	// reservedTokensForSummary reserves token budget for session compact/summary
	// so a large tool result cannot squeeze out the summarizer.
	reservedTokensForSummary int64 = 20_000
	// charsPerToken is a conservative character-to-token ratio.
	// English averages ~3.5-4 chars/token; CJK is lower (~1.5-2).
	// We use 2 to err on the side of truncating earlier rather than blowing the context.
	charsPerToken int64 = 2
)

var (
	buildInTools []providers.ToolDefine
)

type ToolUse struct {
	XMLName   xml.Name `xml:"tool_use"`
	GenID     string   `xml:"id"`
	Name      string   `xml:"name"`
	Arguments string   `xml:"arguments"`
}

func (t *ToolUse) ID() string {
	if t.GenID != "" {
		return t.GenID
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(t.Name))
	_, _ = hasher.Write([]byte(t.Arguments))
	hashValue := hasher.Sum64()
	t.GenID = fmt.Sprintf("call-%s-%d", t.Name, hashValue)
	return t.GenID
}

func toolCall(ctx context.Context, sess *session.Session, use *ToolUse, td *tools.Tool) (_ string, _ bool, retErr error) {
	return toolCallWithInvoker(ctx, sess, use, td, tools.NewInvoker())
}

func toolCallWithInvoker(ctx context.Context, sess *session.Session, use *ToolUse, td *tools.Tool, invoker *tools.Invoker) (_ string, _ bool, retErr error) {
	msg, success, _, err := executeToolCall(ctx, sess, use, td, invoker)
	return msg, success, err
}

func executeToolCall(ctx context.Context, sess *session.Session, use *ToolUse, td *tools.Tool, invoker *tools.Invoker) (_ string, _ bool, _ *tools.Result, retErr error) {
	ctx, span := tracing.Start(ctx, "tools.handler",
		tracing.WithAttributes(
			tracing.String("tool.name", use.Name),
			tracing.TruncateAttr("tool.input", use.Arguments),
			tracing.String("session.id", sess.ID),
			tracing.String("session.root_id", sess.Root.ID),
		),
	)
	defer span.End()
	defer func() { tracing.DeferStatus(span, &retErr) }()

	req := &tools.Request{SessionID: sess.ID, SessionRecords: sess, MaxOutputChars: toolResultLimit(sess)}
	args, ok := common.ParseToolUseArguments(use.Arguments)
	if !ok {
		return "", false, nil, fmt.Errorf("%s", common.FormatToolUseArgumentsError(use.Name, use.Arguments))
	}
	req.Arguments = args
	// Validate the complete advertised schema so handlers only need to enforce
	// domain rules.
	if msg := td.ValidateArguments(args); msg != "" {
		span.RecordError(fmt.Errorf("tool %s %s", use.Name, msg))
		span.SetStatus(tracing.StatusError, "invalid tool arguments")
		result := tools.NewToolResultError(fmt.Sprintf("tool %s: %s", use.Name, msg))
		content, err := marshalToolResultForModel(result)
		if err != nil {
			return "", false, nil, fmt.Errorf("marshal tool %s result failed: %s", use.Name, err)
		}
		return content, false, result, nil
	}

	if invoker == nil {
		invoker = tools.NewInvoker()
	}
	result, err := invoker.Invoke(ctx, td, req)
	if err != nil {
		return "", false, result, err
	}

	content, err := marshalToolResultForModel(result)
	if err != nil {
		return "", false, result, fmt.Errorf("marshal tool %s result failed: %s", use.Name, err)
	}

	msg := truncateToolResult(sess, content)
	span.SetAttributes(tracing.TruncateAttr("tool.output", msg))
	if result.IsError {
		span.SetStatus(tracing.StatusError, "tool returned an error")
	}
	return msg, !result.IsError, result, nil
}

// modelToolResult is the reduced view of tools.Result that is serialized into
// the model-visible tool message. Execution-control fields (Retryable,
// RetryReason, ExitCode, Cancelled) are consumed by the setup layer directly
// from the *tools.Result returned by the handler and would only leak noise
// into the model's context.
type modelToolResult struct {
	Content []tools.Content `json:"content"`
	FYI     string          `json:"fyi,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
}

func marshalToolResultForModel(result *tools.Result) (string, error) {
	if result == nil {
		return "", fmt.Errorf("tool returned a nil result")
	}
	if len(result.Content) == 1 {
		if content, ok := result.Content[0].(tools.TextContent); ok {
			text := content.Text
			if result.IsError {
				if !strings.Contains(strings.ToLower(text), "suggestion:") {
					text += "\nSuggestion: inspect the error, correct the tool arguments or prerequisites, and retry only when it is safe."
				}
				text = "Error: " + text
			}
			if result.FYI != "" {
				text += "\n\nFYI:\n" + result.FYI
			}
			return text, nil
		}
	}
	view := modelToolResult{Content: result.Content, FYI: result.FYI, IsError: result.IsError}
	content, err := json.Marshal(view)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func truncateToolArgs(s string) string {
	const max = 80
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func truncateToolResult(sess *session.Session, content string) string {
	limit := toolResultLimit(sess)
	runes := []rune(content)
	if int64(len(runes)) <= limit {
		return content
	}
	logger.New("tools").Warnw("tool output truncated", "showing", limit, "total", len(runes))
	return fmt.Sprintf("%s\n[Tool output truncated: showing %d of %d chars]",
		string(runes[:limit]), limit, int64(len(runes)))
}

func toolResultLimit(sess *session.Session) int64 {
	limit := defaultMaxToolResultChars
	if st := sess.EnsureContextState(); st.PromptBudget.ContextWindow > 0 {
		remaining := st.PromptBudget.ContextWindow - sess.Tokens() - reservedTokensForSummary
		if remaining > 0 {
			limit = remaining * charsPerToken
		} else {
			limit = minToolResultChars
		}
	}
	if limit > maxSingleToolResultChars {
		limit = maxSingleToolResultChars
	}
	return limit
}

func newLLMRequest(systemMessage string, sess *session.Session, toolList []*tools.Tool) providers.Request {
	var toolDef []providers.ToolDefine
	for _, t := range buildInTools {
		toolDef = append(toolDef, t)
	}

	for _, t := range toolList {
		toolDef = append(toolDef, t)
	}

	req := providers.NewRequest(systemMessage, sess.GetHistory()...)
	req.SetToolDefines(toolDef)
	return req
}
