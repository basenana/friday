package subagents

import (
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
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

		subSession := sess.Fork()
		ctx, span := tracing.Start(ctx, "subagent.call",
			tracing.WithAttributes(
				tracing.String("subagent.name", agentName),
				tracing.String("session.id", subSession.ID),
				tracing.String("parent_session.id", sess.ID),
			),
		)
		defer span.End()

		subTask := injectStructuredReportRequest(userMessage)

		for _, agt := range agents {
			if fuzzyMatching(agt.Name, agentName) {
				req := &api.Request{
					Session:     subSession,
					UserMessage: subTask,
					Tools:       subagentTools,
				}
				content, err := api.ReadAllContent(ctx, agt.Agent.Chat(ctx, req))
				if err != nil {
					return tools.NewToolResultError(err.Error()), nil
				}

				return tools.NewToolResultText(FormatReport(BuildReport(userMessage, content))), nil
			}
		}

		return tools.NewToolResultError(fmt.Sprintf("subagent %s not found", agentName)), nil
	}
}

func injectStructuredReportRequest(task string) string {
	return strings.TrimSpace(task) + `

Return a single final report with these exact sections:
- Task
- What Changed
- Findings
- Files Touched
- Open Questions
- Recommended Next Step

Keep the report concise but specific.`
}
