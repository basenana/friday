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

Each clone is stateless and returns one report. Before calling the tool, identify every investigation that can run independently now and submit all of them together in one tasks array. The runtime queues excess work and keeps all available parallel slots busy, so do not serialize independent investigations or split them into artificial waves. Use a one-item array only when the investigation is genuinely indivisible. Give every item complete context, scope, and requested findings. Put dependent investigations in a later call after their prerequisites finish, and do not invent duplicate or low-value tasks merely to increase parallelism.
</explore>
`

	EXPLORE_DESCRIPTION_PROMPT = `Launch short-lived clones in forked sessions to investigate independent tasks concurrently and return an ordered batch of structured reports.

Use this tool for:
- reading multiple files or tracing execution paths
- broad search, root-cause investigation, or architecture discovery
- gathering facts before you decide or implement

Avoid this tool for:
- trivial single-file lookups
- direct edits or implementation work
- tasks that clearly belong to a named expert

Submit every independent investigation that can run now in one tasks array. The runtime automatically queues work above the active concurrency limit while keeping available slots busy; task count itself is not limited. Do not make several sequential calls for work that has no dependency.

Write each task so it includes:
- the question to answer or issue to investigate
- relevant scope, files, subsystems, or hypotheses when known
- the exact findings you want back in the report

Each clone is stateless. Put all required context in every item. Submit dependent work only after its prerequisite report arrives, avoid duplicate or artificial tasks, and summarize the returned reports to the user.`

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

Each expert task is stateless and returns one result. Before calling the tool, identify every expert task that can run independently now and submit all of them together in one tasks array. The runtime queues excess work and keeps every available parallel slot busy, so do not serialize independent tasks or split them into artificial waves. Use a one-item array only when the work is genuinely indivisible. Give every item full context, constraints, and the exact outcome required. Defer tasks with data dependencies, ordering requirements, or overlapping writes until their prerequisites finish, and do not manufacture duplicate work merely to increase parallelism.
</run_task>
`

	EXPERT_DESCRIPTION_PROMPT = `Delegate independent work to specialized expert agents concurrently and return an ordered batch of results.

Available expert agents:
{available_agents}

Submit every independent expert task that can run now in one tasks array, selecting agent_name separately for each item by matching its task to the agent description. The runtime automatically queues work above the active concurrency limit while keeping available slots busy; task count itself is not limited. Do not serialize independent work across multiple calls.

Use each task to provide:
- the task to complete
- relevant context, constraints, and expected output
- any files, artifacts, or checks the expert should pay attention to

Notes:
1. Each task is stateless, so include all necessary context in every item
2. Batch all independent tasks immediately; use later calls only for real dependencies, ordering requirements, or write conflicts
3. Avoid duplicate, overlapping, artificially narrow, or low-value tasks
4. Successful results remain valid when another item fails; correct and retry only failed items
5. Results are returned only to you; synthesize relevant parts for the user
6. For general investigation or broad research, prefer "explore"
`
)

func initSystemPrompts(opt Option, experts []ExpertAgent) []string {
	var prompts []string
	if opt.SelfAgent != nil && strings.TrimSpace(opt.ExploreSystemPrompt) != "" {
		prompts = append(prompts, opt.ExploreSystemPrompt)
	}
	if len(experts) > 0 && strings.TrimSpace(opt.RunTaskSystemPrompt) != "" {
		prompts = append(prompts, opt.RunTaskSystemPrompt)
	}
	return prompts
}

func initExpertDescriptionPrompt(opt Option, experts []ExpertAgent) string {
	buf := &bytes.Buffer{}
	buf.WriteString("<available_agents>\n")

	for _, agt := range experts {
		buf.WriteString(fmt.Sprintf("<agent_name>%s</agent_name>\n", agt.Name))
		buf.WriteString(fmt.Sprintf("<description>\n%s\n</description>\n", agt.Description))
	}

	buf.WriteString("</available_agents>\n")

	return strings.ReplaceAll(opt.RunTaskDescriptionPrompt, "{available_agents}", buf.String())
}
