package coordinator

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

func fuzzyMatching(s1, s2 string) bool {
	s1 = strings.ToLower(strings.ReplaceAll(s1, " ", ""))
	s2 = strings.ToLower(strings.ReplaceAll(s2, " ", ""))
	return s1 == s2
}

func callSubagentTool(agent agents.Agent, sess *session.Session, agentTools []*tools.Tool) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		userMessage, ok := request.Arguments["task_describe"].(string)
		if !ok {
			return nil, fmt.Errorf("missing required parameter: task_describe")
		}

		req := &api.Request{
			Session:     sess.Fork(),
			UserMessage: userMessage,
			Tools:       agentTools,
		}
		content, err := api.ReadAllContent(ctx, agent.Chat(ctx, req))
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		return tools.NewToolResultText(content), nil
	}
}
