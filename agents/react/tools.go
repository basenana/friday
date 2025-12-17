package react

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash/fnv"

	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/providers/openai"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
)

var (
	buildInTools = []openai.ToolDefine{
		{Name: "topic_finish_close", Description: "If you believe the question has been resolved and has an ultimate answer, " +
			"you must execute the tool to end the topic, otherwise the topic will not end, and the tool does not require input parameters",
			Parameters: map[string]any{"properties": map[string]any{}, "type": "object"}},
	}
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

type ToolUseResult struct {
	XMLName xml.Name `xml:"tool_use_result"`
	Name    string   `xml:"name"`
	Result  string   `xml:"result"`
}

func toolCall(ctx context.Context, session *types.Session, use *ToolUse, extraArgs map[string]string, td *tools.Tool) (string, error) {
	req := &tools.Request{Arguments: make(map[string]interface{}), Session: session}
	if err := json.Unmarshal([]byte(use.Arguments), &req.Arguments); err != nil {
		return "", fmt.Errorf("unmarshal json argument failed: %s", err)
	}
	if extraArgs != nil {
		for k, v := range extraArgs {
			if v == "" {
				continue
			}
			req.Arguments[k] = v
		}
	}

	result, err := td.Handler(ctx, req)
	if err != nil {
		return "", err
	}
	content, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal tool %s result failed: %s", use.Name, err)
	}

	tur := &ToolUseResult{Name: use.Name, Result: string(content)}
	content, err = xml.Marshal(tur)
	if err != nil {
		return "", fmt.Errorf("marshal tool use %s result failed: %s", use.Name, err)
	}
	return string(content), nil
}

func newLLMRequest(systemMessage string, mem *memory.Memory, toolList []*tools.Tool) openai.Request {
	var toolDef []openai.ToolDefine
	for _, t := range toolList {
		toolDef = append(toolDef, openai.ToolDefine{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.JsonSchema(),
		})
	}

	for _, t := range mem.Tools() {
		toolDef = append(toolDef, openai.ToolDefine{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.JsonSchema(),
		})
	}

	for _, t := range buildInTools {
		toolDef = append(toolDef, t)
	}
	return openai.NewToolsRequest(openai.NewSimpleRequest(systemMessage, mem.History()...), toolDef)
}
