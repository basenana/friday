package subagents

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	SIDE_CHECK_SYSTEM_PROMPT = `<run_side_checks>
You can use "run_side_checks" to perform focused, read-only checks that support the current task.

Each check runs in a short-lived fork that inherits a snapshot of the current conversation history, project context, hooks, and session state. The checker already knows the context visible in this conversation. Provide only the additional question, scope, constraints, and expected evidence needed for that check.

Use "run_side_checks" when you need to:
- verify a specific assumption before continuing
- inspect a narrow code path or behavior
- locate a definition, reference, configuration, or existing test
- compare a small number of relevant files
- confirm whether a proposed change has a particular local consequence
- obtain one concrete fact that is missing from the current reasoning

Do not use "run_side_checks" for:
- broad or open-ended research
- general repository exploration without a concrete question
- implementing changes or editing files
- delegating the primary task
- work that clearly belongs to a named expert; use "run_task" instead
- questions that can be answered immediately from the context already available
- dependent checks whose inputs require another check to finish first

A single check is normal and does not need to be split. Submit multiple checks together only when they are genuinely independent and useful to the current task. Every check in one call receives the same parent-context snapshot and cannot see results produced by sibling checks.

Do not create duplicate, artificial, or unnecessarily narrow checks merely to increase parallelism. Prefer the smallest number of checks that can resolve the current uncertainty.

The returned reports are evidence for the parent task. Evaluate and synthesize them instead of repeating them verbatim.
</run_side_checks>
`

	SIDE_CHECK_DESCRIPTION_PROMPT = `Run focused, read-only side checks in short-lived child agents that inherit the current conversation context, and return an ordered batch of structured reports.

Use this tool to verify a concrete assumption, inspect a narrow code path, locate definitions or references, compare a small number of relevant files, confirm local behavior, or obtain a specific fact needed by the current task.

Each checker receives a snapshot of the conversation history and project context available when this tool is called. Do not repeat context that is already visible. Add only the precise question, any extra scope or constraints, and the evidence expected.

A single check is normal. Include multiple checks in one call only when they are independent and can run concurrently. Checks in the same call cannot consume one another's results. Submit dependent checks in a later call after their prerequisites are known.

Do not use this tool for broad research, open-ended repository exploration, implementation work, file modification, delegation of the primary task, or work that belongs to a named expert. Use run_task for specialized expert work.

Keep every check narrowly scoped. Do not create duplicate or artificial checks to increase parallelism.`

	EXPERT_SYSTEM_PROMPT = `<run_task>
You can use "run_task" to delegate work to named expert agents with specialized capabilities.

Use "run_task" when:
- an expert's description clearly matches the task
- an expert says it should be used proactively
- specialized processing is better than handling the work yourself

Do not use "run_task" when:
- the work is a focused, read-only verification or factual check that supports the current task; use "run_side_checks" instead
- the work is broad, open-ended research without a matching expert
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
6. For a focused, read-only verification or factual check that depends on the current conversation context, use run_side_checks
`
)

func initSystemPrompts(opt Option, experts []ExpertAgent) []string {
	var prompts []string
	if opt.SideCheckAgent != nil && strings.TrimSpace(opt.SideCheckSystemPrompt) != "" {
		prompts = append(prompts, opt.SideCheckSystemPrompt)
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
