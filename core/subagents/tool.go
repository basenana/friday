package subagents

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/agents"
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

func callSideCheckTool(sideChecker *ExpertAgent, sess *session.Session, sideCheckTools []*tools.Tool) tools.ToolHandlerFunc {
	return callSideCheckToolWithForker(sideChecker, sess, nil, sideCheckTools, make(chan struct{}, defaultMaxParallelSubagents))
}

func callSideCheckToolWithForker(sideChecker *ExpertAgent, sess *session.Session, forker SessionForker, sideCheckTools []*tools.Tool, parallel chan struct{}) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		if request.SessionID != sess.Root.ID {
			return nestedSubagentError(), nil
		}

		checks, issue := parseSideChecks(request.Arguments)
		if issue != "" {
			return tools.NewToolResultActionableError(
				"Invalid run_side_checks batch:\n"+issue,
				"provide a non-empty checks array whose items are focused, independent, read-only questions, then retry the corrected batch.",
			), nil
		}

		batch := make([]batchTask, len(checks))
		for i, check := range checks {
			batch[i] = batchTask{Agent: "side_check", Task: check}
		}
		results := runBatch(ctx, batch, parallel, func(taskCtx context.Context, index int, task batchTask) batchTaskResult {
			output, err := executeSideCheck(taskCtx, sideChecker, sess, forker, sideCheckTools, task.Task, index, len(batch))
			if err != nil {
				return failedBatchTask(task, err, sideCheckFailureSuggestion(err))
			}
			return successfulBatchTask(task, output)
		})
		return batchToolResult("run_side_checks", results), nil
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

func parseSideChecks(arguments map[string]interface{}) ([]string, string) {
	rawChecks, ok := arguments["checks"].([]any)
	if !ok {
		return nil, "- checks must be an array of strings."
	}
	if len(rawChecks) == 0 {
		return nil, "- checks must contain at least one side check."
	}
	checks := make([]string, len(rawChecks))
	var issues []string
	for i, raw := range rawChecks {
		check, ok := raw.(string)
		if !ok {
			issues = append(issues, fmt.Sprintf("- checks[%d] must be a string.", i))
			continue
		}
		check = strings.TrimSpace(check)
		if check == "" {
			issues = append(issues, fmt.Sprintf("- checks[%d] must not be blank.", i))
			continue
		}
		checks[i] = check
	}
	return checks, strings.Join(issues, "\n")
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

// childTotalTimeout bounds one subagent run as a whole: its loop limit times
// the shared per-loop budget (agents.PerLoopBudget). It is the child's total
// deadline, never a per-API-call one. Agents that do not expose a loop limit
// run under the parent context only.
func childTotalTimeout(agent agents.Agent) (time.Duration, bool) {
	limiter, ok := agent.(interface{ LoopLimit() int })
	if !ok || limiter.LoopLimit() <= 0 {
		return 0, false
	}
	return time.Duration(limiter.LoopLimit()) * agents.PerLoopBudget, true
}

// runContextForChild returns the context for consuming a child agent's Chat
// response, bounded by the child's total timeout when available.
func runContextForChild(ctx context.Context, agent agents.Agent) (context.Context, context.CancelFunc) {
	if timeout, ok := childTotalTimeout(agent); ok {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

func executeSideCheck(ctx context.Context, sideChecker *ExpertAgent, sess *session.Session, forker SessionForker, sideCheckTools []*tools.Tool, check string, index, total int) (output string, retErr error) {
	subSession, release, err := forkSubSession(sess, forker)
	if err != nil {
		return "", fmt.Errorf("create side-check session: %w", err)
	}
	ctx, span := tracing.Start(ctx, "side_check.call", tracing.WithAttributes(
		tracing.TruncateAttr("side_check.input", check),
		tracing.IntVal("batch.index", index+1),
		tracing.IntVal("batch.size", total),
		tracing.String("session.id", subSession.ID),
		tracing.String("parent_session.id", sess.ID),
		tracing.String("session.root_id", subSession.Root.ID),
	))
	defer span.End()
	sess.PublishEvent(subagentEvent(types.EventSubagentStart, "side_check", check, subSession.ID, index, total))
	defer func() {
		if release != nil {
			if err := release(); err != nil {
				if retErr == nil {
					retErr = fmt.Errorf("release side-check session: %w", err)
				} else {
					retErr = fmt.Errorf("%v; release side-check session: %w", retErr, err)
				}
			}
		}
		eventOutput := output
		if retErr != nil {
			eventOutput = retErr.Error()
			span.SetStatus(tracing.StatusError, eventOutput)
		} else {
			span.SetAttributes(tracing.TruncateAttr("side_check.output", output))
			span.SetStatus(tracing.StatusOK, "")
		}
		sess.PublishEvent(subagentEvent(types.EventSubagentFinish, "side_check", eventOutput, subSession.ID, index, total))
	}()

	runCtx, cancelRun := runContextForChild(ctx, sideChecker.Agent)
	defer cancelRun()
	content, err := api.ReadAllContent(runCtx, sideChecker.Agent.Chat(runCtx, &api.Request{
		Session: subSession, UserMessage: injectSideCheckReportRequest(check), Tools: sideCheckTools,
	}))
	if err != nil {
		return "", err
	}
	return FormatReport(BuildSideCheckReport(check, content)), nil
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

	runCtx, cancelRun := runContextForChild(ctx, agent.Agent)
	defer cancelRun()
	content, err := api.ReadAllContent(runCtx, agent.Agent.Chat(runCtx, &api.Request{
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
		"complete every assigned task directly with the tools already available in this subagent; do not call run_side_checks or run_task again.",
	)
}

func sideCheckFailureSuggestion(err error) string {
	if ctxSuggestion := cancellationSuggestion(err); ctxSuggestion != "" {
		return ctxSuggestion
	}
	return "Correct the reported prerequisite and retry only this failed side check; keep all successful reports from the batch."
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

const subagentModePrompt = `<subagent_execution>
You are operating in delegated subagent mode. The parent agent assigned you one focused task, and your responsibility is to complete that task directly and return a thorough, useful result.

- Focus exclusively on the assigned task and its stated scope.
- Work independently using the tools already available to you.
- Do not call run_task or run_side_checks, and do not delegate or dispatch nested subtasks.
- Perform the investigation, implementation, and verification permitted by your current tools and permissions.
- Do not stop at a plan or a description of what could be done; complete as much of the task as possible.
- Return a detailed, evidence-based result. Include important findings, actions taken, files involved, verification performed, remaining risks, and blockers the parent agent needs to know.
- If information is incomplete, make reasonable scoped assumptions and state them clearly instead of abandoning the task.
</subagent_execution>`

func injectSideCheckReportRequest(check string) string {
	return strings.TrimSpace(check) + `

<side_check_execution>
You are performing one focused side check for an ongoing parent task.

The current conversation history and project context have already been provided through the forked session. Investigate only the additional check written above.

- Complete the check directly with the read-only tools available to you.
- Do not modify files or external state.
- Do not call run_side_checks or run_task.
- Do not delegate or create nested subtasks.
- Do not broaden the check into general research.
- Stop once the question is answered with sufficient evidence.
- If the requested fact cannot be verified, explain exactly what evidence is missing.
</side_check_execution>

Return a single final report with these exact sections:
- Task
- Findings
- Files Examined
- Open Questions
- Recommended Next Step

Make the report specific and evidence-based. Include relevant file references, but avoid unrelated narration or exploratory suggestions.`
}

func injectStructuredReportRequest(task string) string {
	return strings.TrimSpace(task) + `

` + subagentModePrompt + `

Return a single final report with these exact sections:
- Task
- What Changed
- Findings
- Files Touched
- Open Questions
- Recommended Next Step

Make the report thorough and specific while avoiding irrelevant narration.`
}
