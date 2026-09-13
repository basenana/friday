package planning

const (
	DEFAULT_TASK_DESCRIPTION_PROMPT = `Replace the session's entire todo list and publish the updated state.

Use this tool for multi-step work that benefits from visible progress tracking.
Each call replaces the complete current list, so include every task that should
remain. Use an empty list to clear all todos. Do not call this tool multiple
times in parallel.

Each item requires:
- description: one actionable sentence that includes the expected outcome
- status: pending, in_progress, completed, or blocked

Use blocked only when progress requires user input, approval, an external
system, or another dependency. Return tasks to pending or in_progress after
the blocker is resolved.

Returns the normalized current todo list.`

	DEFAULT_PLANNING_PROMPT = `<write_todos>
Use write_todos for multi-step work that benefits from visible progress. Skip it
for simple requests. Keep the complete list current as work changes, mark tasks
completed promptly, and never call write_todos concurrently.
</write_todos>
`
)
