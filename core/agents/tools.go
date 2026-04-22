package agents

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash/fnv"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
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

	req := &tools.Request{Arguments: make(map[string]interface{}), SessionID: sess.ID}
	if err := json.Unmarshal([]byte(use.Arguments), &req.Arguments); err != nil {
		return "", false, fmt.Errorf("unmarshal json argument failed: %s", err)
	}

	result, err := td.Handler(ctx, req)
	if err != nil {
		return "", false, err
	}

	content, err := json.Marshal(result)
	if err != nil {
		return "", false, fmt.Errorf("marshal tool %s result failed: %s", use.Name, err)
	}

	span.SetAttributes(tracing.TruncateAttr("tool.output", string(content)))
	return string(content), !result.IsError, nil
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
