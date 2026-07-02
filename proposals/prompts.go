package proposals

// PlanPromptTmpl is the user message sent to the planning agent.
// It receives the full PROPOSAL.md design doc and asks for a structured DAG.
const PlanPromptTmpl = `You are the planning phase of a Proposal. Read the design doc below and split the work into a task DAG.

# Proposal

{{.DesignDoc}}

# Output format

Return ONLY a JSON array (no prose, no markdown fences) of tasks with this shape:

  {
    "id": "T01",
    "title": "short imperative title",
    "assignee": "member name or self",
    "deps": ["T00"]
  }

Rules:
- IDs are "T" + zero-padded 2-digit sequence (T01, T02, ...).
- deps reference earlier task IDs. Use [] for root tasks.
- Order the array topologically.
- Keep titles short. Details belong in the task brief, not here.
- Do not include extraneous fields.

Return only the JSON array.`

// ExecutePromptTmpl is the user message for the execute phase. {{.TaskDoc}} is
// the task brief (T##.md content). {{.TaskID}} identifies the task.
const ExecutePromptTmpl = `Execute task {{.TaskID}} of the active proposal.

# Task brief

{{.TaskDoc}}

When finished, return a concise report describing what you did and any findings.`

// ReviewPromptTmpl is the user message for the review phase.
// {{.TaskDoc}} is the brief, {{.Result}} is the executor's output.
const ReviewPromptTmpl = `Review the execution of task {{.TaskID}}.

# Task brief

{{.TaskDoc}}

# Executor result

{{.Result}}

Decide ONE outcome. Return ONLY a JSON object (no markdown fences):

  {"status": "approved",   "comment": "..."}
  {"status": "rejected",   "comment": "..."}
  {"status": "failed",     "comment": "..."}

- approved: result meets the brief.
- rejected: result is salvageable but needs another try; explain what to fix.
- failed: result is unrecoverable; explain why.

Return only the JSON object.`
