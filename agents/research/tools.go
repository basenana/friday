package research

import (
	"context"
	"github.com/basenana/friday/utils"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/agents/agtapi"
	"github.com/basenana/friday/tools"
)

func newResearchTool(agt *Agent) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool(
			"run_blocking_subagents",
			tools.WithDescription("Submit multiple independent tasks, each task will launch a subagent to conduct research in parallel."),
			tools.WithArray("task_describe_list",
				tools.Required(),
				tools.Items(map[string]interface{}{"type": "string", "description": "The item description must be specific, measurable, achievable, and strongly related to the goal."}),
				tools.Description("The task description needs to be executable and have assessable completion conditions."),
			),
			tools.WithString("reasoning",
				tools.Required(),
				tools.Description("The reason and purpose of creating a sub-agent"),
			),
			tools.WithToolHandler(agt.runBlockingsSubagentHandler),
		),
	}
}

func (a *Agent) runBlockingsSubagentHandler(ctx context.Context, request *tools.Request) (*tools.Result, error) {
	tasks, ok := request.Arguments["task_describe_list"].([]any)
	if !ok || len(tasks) == 0 {
		return tools.NewToolResultError("missing required parameter: task_describe_list"), nil
	}
	var taskDescList []string
	for _, taskDescStr := range tasks {
		taskDesc, ok := taskDescStr.(string)
		if !ok {
			return tools.NewToolResultError("task_describe_list must be a string array"), nil
		}
		taskDescList = append(taskDescList, taskDesc)
	}

	var (
		agt     = a.newReAct("subagent", "research worker", promptWithMoreInfo(a.opt.SubAgentPrompt), a.opt.Tools)
		wg      = sync.WaitGroup{}
		result  = make(chan string, len(tasks))
		reports []string
	)

	for _, t := range taskDescList {
		wg.Add(1)
		go func(task string) {
			var (
				startAt = time.Now()
				mem     = agtapi.MemoryFromContext(ctx).Copy()
			)

			a.logger.Infof("subagent task: %s", task)
			defer wg.Done()
			content, err := agtapi.ReadAllContent(ctx, agt.Chat(ctx, &agtapi.Request{
				UserMessage: task,
				Memory:      mem,
			}))
			if err != nil {
				a.logger.Warnw("run subagent task failed", "task", task, "err", err)
				result <- strings.Join(
					[]string{"Subagent Task:", task, task, "Report:", content, "Error:", err.Error()}, "\n")
				return
			}

			result <- strings.Join([]string{"Subagent Task:", task, task, "Report:", content}, "\n")
			a.logger.Infow("subagent task finish", "task", task, "elapsed", time.Since(startAt).String())
		}(t)
	}
	wg.Wait()
	close(result)

	for content := range result {
		reports = append(reports, content)
	}

	return tools.NewToolResultText(utils.Res2Str(reports)), nil
}
