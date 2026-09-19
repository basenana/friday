# planning — TODO tracking, approved plans, and LATS search

This package provides planning tools and hooks: visible TODO state, plan artifacts, request-local approved-plan context, terminal plan submission, and the optional Language Agent Tree Search implementation.

## Files

- `todo.go` defines TODO items, state, hook injection, and whole-list replacement.
- `tools.go` builds `write_todos`, plan submission, and related tool handlers.
- `prompt.go` contains planning-mode guidance.
- `artifact.go` defines approved plan storage contracts.
- `context.go` injects an approved plan into request-local context.
- `terminal.go` marks plan submission as a terminal tool result.
- `lats/lats.go` implements tree-search agent orchestration.
- `lats/tree.go` defines nodes, scores, selection, and expansion.
- `lats/prompts.go` contains LATS prompts.

## Key API

```go
func New(option Option) *Todo
func (a *Todo) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error
func NewApprovedPlanContextHook(plans Repository) *ApprovedPlanContextHook

// core/planning/lats
func New(llm providers.Client, worker agents.Agent, opt Option) *Agent
```

`Repository` in `artifact.go` is the persistence boundary for accepted plans. Keep storage concerns behind it.

## TODO invariants

- `write_todos` replaces the complete list; it is not a patch operation.
- Every item has a description and one valid status: pending, in_progress, completed, or blocked.
- The hook publishes the normalized current list so TUI/card consumers see one authoritative snapshot.
- TODO mutation is disabled while collaboration is in Plan mode, where the approved-plan workflow is authoritative.
- Do not silently preserve omitted items during replacement.

## Plan-mode behavior

- `core/collaboration` defines default and plan collaboration-mode contracts; planning consumes that mode rather than inventing another session flag.
- Approved plan text is injected request-locally through `core/promptcontext`, not appended permanently to ordinary history.
- `submit_plan` is terminal for the current model turn: after successful submission, the agent must not issue follow-up tools or prose in that turn.
- Plan artifacts need stable IDs/association with the owning session.
- Reject empty or malformed submissions before updating repository state.

## LATS

- LATS explores candidate reasoning/action branches with bounded depth, width, and iteration defaults from `lats.Option`.
- Selection and scoring must be deterministic for equal scores where tests require it.
- A terminal successful node ends search; exhausted search returns its best available result with an explicit outcome.
- Worker/provider cancellation propagates through tree expansion.
- Keep tree mechanics in `tree.go` independent from provider prompt construction.

## Tests

Run:

```bash
go test ./core/planning/...
```

Tests are local and use fakes. Add coverage for whole-list replacement, mode gating, request-local plan injection, terminal submission, and LATS bounds when changing behavior.
