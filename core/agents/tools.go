package agents

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash/fnv"

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

	req := &tools.Request{SessionID: sess.ID}
	args, ok := common.ParseToolUseArguments(use.Arguments)
	if !ok {
		return "", false, fmt.Errorf("tool %s arguments must be a JSON object, got: %.80s", use.Name, use.Arguments)
	}
	req.Arguments = args

	result, err := td.Handler(ctx, req)
	if err != nil {
		return "", false, err
	}

	content, err := json.Marshal(result)
	if err != nil {
		return "", false, fmt.Errorf("marshal tool %s result failed: %s", use.Name, err)
	}

	msg := truncateToolResult(sess, string(content))
	span.SetAttributes(tracing.TruncateAttr("tool.output", msg))
	return msg, !result.IsError, nil
}

func truncateToolResult(sess *session.Session, content string) string {
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
	runes := []rune(content)
	if int64(len(runes)) <= limit {
		return content
	}
	logger.New("tools").Warnw("tool output truncated", "showing", limit, "total", len(runes))
	return fmt.Sprintf("%s\n[Tool output truncated: showing %d of %d chars]",
		string(runes[:limit]), limit, int64(len(runes)))
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
