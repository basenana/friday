package subagents

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

func fuzzyMatching(s1, s2 string) bool {
	s1 = strings.ToLower(strings.ReplaceAll(s1, " ", ""))
	s2 = strings.ToLower(strings.ReplaceAll(s2, " ", ""))
	return s1 == s2
}

func callSubagentTool(agents []ExpertAgent, sess *session.Session, subagentTools []*tools.Tool) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		agentName, ok := request.Arguments["agent_name"].(string)
		if !ok || agentName == "" {
			return nil, fmt.Errorf("missing required parameter: agent_name")
		}

		userMessage, ok := request.Arguments["task_describe"].(string)
		if !ok || userMessage == "" {
			return nil, fmt.Errorf("missing required parameter: task_describe")
		}

		for _, agt := range agents {
			if agt.Name == agentName {
				req := &api.Request{
					Session:     sess.Fork(),
					UserMessage: userMessage,
					Tools:       subagentTools,
				}
				content, err := api.ReadAllContent(ctx, agt.Agent.Chat(ctx, req))
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText(content), nil
			}
		}

		return tools.NewToolResultError(fmt.Sprintf("subagent %s not found", agentName)), nil
	}
}
