package research

import (
	"context"
	"strings"
	"sync"

	"github.com/basenana/friday/core/agents"
	agtapi "github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
)

func newResearchLeader(agt *Agent, sess *session.Session, agentTools []*tools.Tool) agents.Agent {
	leaderTools := newLeaderTool(agt.worker, sess, agentTools, agt.opt)
	leaderTools = append(leaderTools, agentTools...)
	return agents.New(agt.llm, agents.Option{
		SystemPrompt: promptWithMoreInfo(agt.opt.LeaderPrompt),
		MaxLoopTimes: agt.opt.MaxResearchLoopTimes,
		Tools:        leaderTools,
	})
}

func newLeaderTool(worker agents.Agent, sess *session.Session, agentTools []*tools.Tool, option Option) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool(
			"run_blocking_subagents",
			tools.WithDescription(DEFAULT_RUN_SUBAGENT_DESCRIPTION_PROMPT),
			tools.WithArray("tasks",
				tools.Required(),
				tools.MinItems(1),
				tools.Items(map[string]interface{}{"type": "string", "minLength": 1}),
				tools.Description("Independent task instructions. Each item must include scope, expected output, and completion criteria."),
			),
			tools.WithExample(map[string]interface{}{"tasks": []interface{}{"Compare provider tool-schema serialization and report inconsistencies with file references.", "Audit built-in array arguments and report any schema deeper than two container levels."}}),
			tools.WithToolHandler(blockingSubagentTool(worker, sess, agentTools)),
		),
	}
}

func blockingSubagentTool(worker agents.Agent, sess *session.Session, agentTools []*tools.Tool) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		tasks, ok := request.Arguments["tasks"].([]any)
		if !ok {
			if _, present := request.Arguments["tasks"]; present {
				return tools.NewToolResultError("tasks must be a string array"), nil
			}
			return tools.NewToolResultError("missing required parameter: tasks"), nil
		}
		if len(tasks) == 0 {
			return tools.NewToolResultError("tasks must contain at least one task"), nil
		}
		var taskList []string
		for _, raw := range tasks {
			task, ok := raw.(string)
			if !ok {
				return tools.NewToolResultError("tasks must be a string array"), nil
			}
			taskList = append(taskList, task)
		}

		var (
			result  = make(chan string, len(tasks))
			reports []string
		)

		subRoot := sess.Fork()
		tracing.SpanFromContext(ctx).AddEvent("session.fork",
			tracing.String("session.id", subRoot.ID),
			tracing.String("parent_session.id", sess.ID),
			tracing.String("session.root_id", subRoot.Root.ID),
			tracing.String("source", "research.batch"),
		)

		var workers sync.WaitGroup
		for _, task := range taskList {
			workers.Add(1)
			go func(task string) {
				defer workers.Done()
				subSession := subRoot.Fork()
				tracing.SpanFromContext(ctx).AddEvent("session.fork",
					tracing.String("session.id", subSession.ID),
					tracing.String("parent_session.id", subRoot.ID),
					tracing.String("session.root_id", subSession.Root.ID),
					tracing.String("source", "research.task"),
				)
				content, err := agtapi.ReadAllContent(ctx, worker.Chat(ctx, &agtapi.Request{
					Session:     subSession,
					UserMessage: injectResearchReportRequest(task),
					Tools:       agentTools,
				}))
				if err != nil {
					result <- strings.Join(
						[]string{"Subagent Task:", task, task, "Report:", content, "Error:", err.Error()}, "\n")
					return
				}

				result <- subagents.FormatReport(subagents.BuildReport(task, content))
			}(task)
		}
		workers.Wait()
		close(result)

		for content := range result {
			reports = append(reports, content)
		}

		return tools.NewToolResultText(tools.Res2Str(reports)), nil
	}
}

func NewDefaultWorker(llm providers.Client, opt Option) agents.Agent {
	return agents.New(llm, agents.Option{
		SystemPrompt: SUBAGENT_PROMPT,
		MaxLoopTimes: 30,
		Tools:        opt.ResearchTools,
	})
}

func injectResearchReportRequest(task string) string {
	return strings.TrimSpace(task) + `

Return a final report with these sections:
- Task
- What Changed
- Findings
- Files Touched
- Open Questions
- Recommended Next Step`
}
