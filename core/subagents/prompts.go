package subagents

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	EXPLORE_SYSTEM_PROMPT = `<explore>
You can use "explore" to launch a short-lived clone in a forked session for research and codebase investigation.

Use "explore" when:
- the answer requires reading multiple files, tracing call paths, or broad search
- you want to keep exploratory work out of the main thread
- you only need findings, not intermediate steps

Do not use "explore" when:
- the lookup is trivial or limited to one or two obvious files
- you need to make changes directly
- a named expert is a better fit; use "run_task" for specialized expert work

The clone is stateless and returns one report. Give complete context, request specific findings, and parallelize independent explore tasks when useful.
</explore>
`

	EXPLORE_DESC_PROMPT = `Launch a short-lived clone in a forked session to investigate and return a single structured report.

Use this tool for:
- reading multiple files or tracing execution paths
- broad search, root-cause investigation, or architecture discovery
- gathering facts before you decide or implement

Avoid this tool for:
- trivial single-file lookups
- direct edits or implementation work
- tasks that clearly belong to a named expert

Write task_describe so it includes:
- the question to answer or issue to investigate
- relevant scope, files, subsystems, or hypotheses when known
- the exact findings you want back in the report

The clone is stateless. Put all required context in the request. Its report comes back only to you, so summarize relevant findings to the user.`

	EXPERT_SYSTEM_PROMPT = `<run_task>
You can use "run_task" to delegate work to named expert agents with specialized capabilities.

Use "run_task" when:
- an expert's description clearly matches the task
- an expert says it should be used proactively
- specialized processing is better than handling the work yourself

Do not use "run_task" when:
- the work is general exploration; use "explore" instead
- the task is trivial
- no expert description matches the need

Each expert call is stateless and returns one result. Provide full context, constraints, and the exact outcome you want back.
</run_task>
`

	EXPERT_DESC_PROMPT = `Delegate work to a specialized expert agent.

Available expert agents:
{available_agents}

Select agent_name by matching the task to the agent's describe text. Do not guess from the name alone.

Use task_describe to provide:
- the task to complete
- relevant context, constraints, and expected output
- any files, artifacts, or checks the expert should pay attention to

Notes:
1. Each expert call is stateless, so include all necessary context
2. Use parallel expert calls only for independent tasks
3. The expert's result is returned only to you; summarize relevant parts to the user
4. For general investigation or broad research, prefer "explore"
`
)

func initSystemPrompts(opt Option) []string {
	var prompts []string
	if opt.SelfAgent != nil && strings.TrimSpace(opt.ExploreSystemPrompt) != "" {
		prompts = append(prompts, opt.ExploreSystemPrompt)
	}
	if len(opt.ExpertAgents) > 0 && strings.TrimSpace(opt.RunTaskSystemPrompt) != "" {
		prompts = append(prompts, opt.RunTaskSystemPrompt)
	}
	return prompts
}

func initExpertDescribePrompt(opt Option) string {
	buf := &bytes.Buffer{}
	buf.WriteString("<available_agents>\n")

	for _, agt := range opt.ExpertAgents {
		buf.WriteString(fmt.Sprintf("<agent_name>%s</agent_name>\n", agt.Name))
		buf.WriteString(fmt.Sprintf("<describe>\n%s\n</describe>\n", agt.Describe))
	}

	buf.WriteString("</available_agents>\n")

	return strings.ReplaceAll(opt.RunTaskDescribePrompt, "{available_agents}", buf.String())
}
