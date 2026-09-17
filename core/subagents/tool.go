package subagents

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
)

func fuzzyMatching(s1, s2 string) bool {
	s1 = strings.ToLower(strings.ReplaceAll(s1, " ", ""))
	s2 = strings.ToLower(strings.ReplaceAll(s2, " ", ""))
	return s1 == s2
}

func callExploreTool(self *ExpertAgent, sess *session.Session, exploreTools []*tools.Tool) tools.ToolHandlerFunc {
	return callExploreToolWithForker(self, sess, nil, exploreTools, make(chan struct{}, defaultMaxParallelSubagents))
}

func callExploreToolWithForker(self *ExpertAgent, sess *session.Session, forker SessionForker, exploreTools []*tools.Tool, parallel chan struct{}) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		if request.SessionID != sess.Root.ID {
			return nestedSubagentError(), nil
		}

		tasks, issue := parseExploreTasks(request.Arguments)
		if issue != "" {
			return tools.NewToolResultActionableError(
				"Invalid explore batch:\n"+issue,
				"provide a non-empty tasks array whose items are complete, independent investigation requests, then retry the corrected batch.",
			), nil
		}

		batch := make([]batchTask, len(tasks))
		for i, task := range tasks {
			batch[i] = batchTask{Agent: "explore", Task: task}
		}
		results := runBatch(ctx, batch, parallel, func(taskCtx context.Context, index int, task batchTask) batchTaskResult {
			output, err := executeExploreTask(taskCtx, self, sess, forker, exploreTools, task.Task, index, len(batch))
			if err != nil {
				return failedBatchTask(task, err, exploreFailureSuggestion(err))
			}
			return successfulBatchTask(task, output)
		})
		return batchToolResult("explore", results), nil
	}
}

func callSubagentTool(agents []ExpertAgent, sess *session.Session, subagentTools []*tools.Tool) tools.ToolHandlerFunc {
	return callSubagentToolWithForker(agents, sess, nil, subagentTools, make(chan struct{}, defaultMaxParallelSubagents))
}

func callSubagentToolWithForker(agents []ExpertAgent, sess *session.Session, forker SessionForker, subagentTools []*tools.Tool, parallel chan struct{}) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		if request.SessionID != sess.Root.ID {
			return nestedSubagentError(), nil
		}

		batch, issue := parseExpertTasks(request.Arguments)
		if issue != "" {
			return tools.NewToolResultActionableError(
				"Invalid run_task batch:\n"+issue,
				"provide a non-empty tasks array and give every item a valid agent_name plus a complete, independent task, then retry the corrected batch.",
			), nil
		}

		results := runBatch(ctx, batch, parallel, func(taskCtx context.Context, index int, task batchTask) batchTaskResult {
			output, err := executeExpertTask(taskCtx, agents, sess, forker, subagentTools, task, index, len(batch))
			if err != nil {
				return failedBatchTask(task, err, expertFailureSuggestion(err))
			}
			return successfulBatchTask(task, output)
		})
		return batchToolResult("run_task", results), nil
	}
}

type batchTask struct {
	Agent string
	Task  string
}

type batchTaskResult struct {
	batchTask
	Status     string
	Output     string
	Error      string
	Suggestion string
}

func parseExploreTasks(arguments map[string]interface{}) ([]string, string) {
	rawTasks, ok := arguments["tasks"].([]any)
	if !ok {
		return nil, "- tasks must be an array of strings."
	}
	if len(rawTasks) == 0 {
		return nil, "- tasks must contain at least one investigation."
	}
	tasks := make([]string, len(rawTasks))
	var issues []string
	for i, raw := range rawTasks {
		task, ok := raw.(string)
		if !ok {
			issues = append(issues, fmt.Sprintf("- tasks[%d] must be a string.", i))
			continue
		}
		task = strings.TrimSpace(task)
		if task == "" {
			issues = append(issues, fmt.Sprintf("- tasks[%d] must not be blank.", i))
			continue
		}
		tasks[i] = task
	}
	return tasks, strings.Join(issues, "\n")
}

func parseExpertTasks(arguments map[string]interface{}) ([]batchTask, string) {
	rawTasks, ok := arguments["tasks"].([]any)
	if !ok {
		return nil, "- tasks must be an array of objects."
	}
	if len(rawTasks) == 0 {
		return nil, "- tasks must contain at least one expert task."
	}
	tasks := make([]batchTask, len(rawTasks))
	var issues []string
	for i, raw := range rawTasks {
		item, ok := raw.(map[string]interface{})
		if !ok {
			issues = append(issues, fmt.Sprintf("- tasks[%d] must be an object with agent_name and task.", i))
			continue
		}
		agentName, agentOK := item["agent_name"].(string)
		task, taskOK := item["task"].(string)
		agentName = strings.TrimSpace(agentName)
		task = strings.TrimSpace(task)
		if !agentOK || agentName == "" {
			issues = append(issues, fmt.Sprintf("- tasks[%d].agent_name must not be blank.", i))
		}
		if !taskOK || task == "" {
			issues = append(issues, fmt.Sprintf("- tasks[%d].task must not be blank.", i))
		}
		tasks[i] = batchTask{Agent: agentName, Task: task}
	}
	return tasks, strings.Join(issues, "\n")
}

func runBatch(ctx context.Context, tasks []batchTask, parallel chan struct{}, execute func(context.Context, int, batchTask) batchTaskResult) []batchTaskResult {
	if parallel == nil || cap(parallel) == 0 {
		parallel = make(chan struct{}, defaultMaxParallelSubagents)
	}
	results := make([]batchTaskResult, len(tasks))
	jobs := make(chan int, len(tasks))
	for i := range tasks {
		jobs <- i
	}
	close(jobs)

	workerCount := min(len(tasks), cap(parallel))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				select {
				case parallel <- struct{}{}:
					if err := ctx.Err(); err != nil {
						<-parallel
						results[index] = cancelledBatchTask(tasks[index], err)
						continue
					}
					results[index] = execute(ctx, index, tasks[index])
					<-parallel
				case <-ctx.Done():
					results[index] = cancelledBatchTask(tasks[index], ctx.Err())
				}
			}
		}()
	}
	workers.Wait()
	return results
}

func successfulBatchTask(task batchTask, output string) batchTaskResult {
	return batchTaskResult{batchTask: task, Status: "succeeded", Output: output}
}

func failedBatchTask(task batchTask, err error, suggestion string) batchTaskResult {
	return batchTaskResult{batchTask: task, Status: "failed", Error: err.Error(), Suggestion: suggestion}
}

func cancelledBatchTask(task batchTask, err error) batchTaskResult {
	message := "batch context was cancelled before this task started"
	if err != nil {
		message += ": " + err.Error()
	}
	return batchTaskResult{
		batchTask:  task,
		Status:     "not_started/cancelled",
		Error:      message,
		Suggestion: "Do not assume this task produced changes. Submit it again only if the parent request is still active.",
	}
}

func batchToolResult(toolName string, results []batchTaskResult) *tools.Result {
	output, succeeded := formatBatchResults(toolName, results)
	if succeeded == 0 {
		return tools.NewToolResultActionableError(output, "review every task error, correct the reported prerequisites or arguments, and resubmit only tasks that are safe to retry.")
	}
	return tools.NewToolResultText(output)
}

func formatBatchResults(toolName string, results []batchTaskResult) (string, int) {
	succeeded := 0
	for _, result := range results {
		if result.Status == "succeeded" {
			succeeded++
		}
	}
	failed := len(results) - succeeded
	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, "<subagent_batch tool=%q total=%q succeeded=%q failed=%q>\n", toolName, strconv.Itoa(len(results)), strconv.Itoa(succeeded), strconv.Itoa(failed))
	buf.WriteString("## Batch Summary\n")
	fmt.Fprintf(buf, "%d task(s) succeeded; %d task(s) failed or did not start.\n", succeeded, failed)
	for i, result := range results {
		fmt.Fprintf(buf, "\n<task_result index=%q status=%q agent=%q>\n", strconv.Itoa(i+1), result.Status, result.Agent)
		buf.WriteString("## Task\n")
		buf.WriteString(result.Task)
		buf.WriteString("\n\n")
		if result.Status == "succeeded" {
			buf.WriteString(result.Output)
			buf.WriteString("\n")
		} else {
			buf.WriteString("## Error\n")
			buf.WriteString(result.Error)
			buf.WriteString("\n\n## Suggested Recovery\n")
			buf.WriteString(result.Suggestion)
			buf.WriteString("\n")
		}
		buf.WriteString("</task_result>\n")
	}
	buf.WriteString("\n## Next Action\n")
	if succeeded > 0 && failed > 0 {
		buf.WriteString("Use the successful reports now. Correct and retry only the failed tasks; do not repeat tasks that already succeeded.\n")
	} else if succeeded > 0 {
		buf.WriteString("Synthesize the successful reports and continue the parent task.\n")
	} else {
		buf.WriteString("No task completed successfully. Follow each recovery instruction before retrying.\n")
	}
	buf.WriteString("</subagent_batch>")
	return strings.TrimSpace(buf.String()), succeeded
}

func executeExploreTask(ctx context.Context, self *ExpertAgent, sess *session.Session, forker SessionForker, exploreTools []*tools.Tool, task string, index, total int) (output string, retErr error) {
	subSession, release, err := forkSubSession(sess, forker)
	if err != nil {
		return "", fmt.Errorf("create explore session: %w", err)
	}
	ctx, span := tracing.Start(ctx, "explore.call", tracing.WithAttributes(
		tracing.TruncateAttr("explore.input", task),
		tracing.IntVal("batch.index", index+1),
		tracing.IntVal("batch.size", total),
		tracing.String("session.id", subSession.ID),
		tracing.String("parent_session.id", sess.ID),
		tracing.String("session.root_id", subSession.Root.ID),
	))
	defer span.End()
	sess.PublishEvent(subagentEvent(types.EventSubagentStart, "explore", task, subSession.ID, index, total))
	defer func() {
		if release != nil {
			if err := release(); err != nil {
				if retErr == nil {
					retErr = fmt.Errorf("release explore session: %w", err)
				} else {
					retErr = fmt.Errorf("%v; release explore session: %w", retErr, err)
				}
			}
		}
		eventOutput := output
		if retErr != nil {
			eventOutput = retErr.Error()
			span.SetStatus(tracing.StatusError, eventOutput)
		} else {
			span.SetAttributes(tracing.TruncateAttr("explore.output", output))
			span.SetStatus(tracing.StatusOK, "")
		}
		sess.PublishEvent(subagentEvent(types.EventSubagentFinish, "explore", eventOutput, subSession.ID, index, total))
	}()

	content, err := api.ReadAllContent(ctx, self.Agent.Chat(ctx, &api.Request{
		Session: subSession, UserMessage: injectExploreReportRequest(task), Tools: exploreTools,
	}))
	if err != nil {
		return "", err
	}
	return FormatReport(BuildExploreReport(task, content)), nil
}

func executeExpertTask(ctx context.Context, agents []ExpertAgent, sess *session.Session, forker SessionForker, subagentTools []*tools.Tool, task batchTask, index, total int) (output string, retErr error) {
	agent := findExpertAgent(agents, task.Agent)
	if agent == nil {
		return "", fmt.Errorf("subagent %q not found; available agents: %s", task.Agent, strings.Join(expertAgentNames(agents), ", "))
	}
	subSession, release, err := forkSubSession(sess, forker)
	if err != nil {
		return "", fmt.Errorf("create session for subagent %q: %w", task.Agent, err)
	}
	ctx, span := tracing.Start(ctx, "subagent.call", tracing.WithAttributes(
		tracing.String("subagent.name", task.Agent),
		tracing.TruncateAttr("subagent.input", task.Task),
		tracing.IntVal("batch.index", index+1),
		tracing.IntVal("batch.size", total),
		tracing.String("session.id", subSession.ID),
		tracing.String("parent_session.id", sess.ID),
		tracing.String("session.root_id", subSession.Root.ID),
	))
	defer span.End()
	sess.PublishEvent(subagentEvent(types.EventSubagentStart, task.Agent, task.Task, subSession.ID, index, total))
	defer func() {
		if release != nil {
			if err := release(); err != nil {
				if retErr == nil {
					retErr = fmt.Errorf("release session for subagent %q: %w", task.Agent, err)
				} else {
					retErr = fmt.Errorf("%v; release session for subagent %q: %w", retErr, task.Agent, err)
				}
			}
		}
		eventOutput := output
		if retErr != nil {
			eventOutput = retErr.Error()
			span.SetStatus(tracing.StatusError, eventOutput)
		} else {
			span.SetAttributes(tracing.TruncateAttr("subagent.output", output))
			span.SetStatus(tracing.StatusOK, "")
		}
		sess.PublishEvent(subagentEvent(types.EventSubagentFinish, task.Agent, eventOutput, subSession.ID, index, total))
	}()

	content, err := api.ReadAllContent(ctx, agent.Agent.Chat(ctx, &api.Request{
		Session: subSession, UserMessage: injectStructuredReportRequest(task.Task), Tools: subagentTools,
	}))
	if err != nil {
		return "", err
	}
	return FormatReport(BuildReport(task.Task, content)), nil
}

func forkSubSession(sess *session.Session, forker SessionForker) (*session.Session, func() error, error) {
	if forker == nil {
		return sess.Fork(), nil, nil
	}
	subSession, err := forker.Fork()
	if err != nil {
		return nil, nil, err
	}
	return subSession, func() error { return forker.Release(subSession) }, nil
}

func findExpertAgent(agents []ExpertAgent, name string) *ExpertAgent {
	for i := range agents {
		if fuzzyMatching(agents[i].Name, name) {
			return &agents[i]
		}
	}
	return nil
}

func subagentEvent(eventType types.EventType, agent, value, sessionID string, index, total int) types.Event {
	data := map[string]string{
		"agent": agent, "session_id": sessionID,
		"batch_index": strconv.Itoa(index + 1), "batch_size": strconv.Itoa(total),
	}
	if eventType == types.EventSubagentStart {
		data["input"] = value
	} else {
		data["output"] = value
	}
	return types.Event{Type: eventType, Data: data}
}

func nestedSubagentError() *tools.Result {
	return tools.NewToolResultActionableError(
		"Subagent mode active: nested subagent creation is not supported.",
		"complete every assigned task directly with the tools already available in this subagent; do not call explore or run_task again.",
	)
}

func exploreFailureSuggestion(err error) string {
	if ctxSuggestion := cancellationSuggestion(err); ctxSuggestion != "" {
		return ctxSuggestion
	}
	return "Correct the reported exploration prerequisite and retry only this failed investigation; keep all successful reports from the batch."
}

func expertFailureSuggestion(err error) string {
	if ctxSuggestion := cancellationSuggestion(err); ctxSuggestion != "" {
		return ctxSuggestion
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return "Choose one of the listed available agents and resubmit only this failed task."
	}
	return "Inspect files and external state before retrying because the expert task may have produced side effects; resubmit only this failed task when it is safe."
}

func cancellationSuggestion(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "canceled") || strings.Contains(lower, "deadline exceeded") {
		return "The task may have partially run. Verify files and external state before deciding whether it is safe to retry; do not repeat successful tasks."
	}
	return ""
}

func injectExploreReportRequest(task string) string {
	return strings.TrimSpace(task) + `

Return a single final report with these exact sections:
- Task (what was asked)
- Findings (key discoveries, organized by topic)
- Files Examined (list of files read or searched, with brief notes on what was found)
- Open Questions (things that remain unclear or need further investigation)
- Recommended Next Step (what the caller should do with these findings)

Keep the report concise but specific. Focus on facts and evidence, not speculation.`
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
