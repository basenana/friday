# subagents — Forked expert delegation and parallel exploration

This package injects the `explore` and `run_task` tools into agents, forks session context for delegated work, enforces shared concurrency limits, and formats structured child reports for the parent conversation.

## Files

- `hook.go` defines options, expert providers, hook injection, and tool construction.
- `tool.go` implements batched exploration and expert execution.
- `report.go` defines child result/status reports and rendering.
- `prompts.go` defines delegation and anti-recursion instructions.
- Tests cover dynamic catalogs, ordering, partial failure, concurrency, and reporting.

## Key contracts

```go
type SessionForker interface {
    Fork() (*session.Session, error)
    Release(*session.Session) error
}

type AgentProvider interface {
    List() []ExpertAgent
}

func NewHook(_ providers.Client, opt Option) *Subagents
func (a *Subagents) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error
func (a *Subagents) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error
```

The hook reads `AgentProvider` for every invocation so long-lived sessions see agent-catalog reloads.

## Delegation invariants

- The default shared limit is four running child agents. `explore` and `run_task` consume the same semaphore.
- One tool invocation may contain multiple independent tasks; execution can be concurrent but returned reports preserve input order.
- Partial success is retained. One child failure does not erase successful sibling reports.
- Each child works in a session fork and that fork is released on every success, failure, and cancellation path.
- A child receives only the tools selected by the caller's policy. Delegation must never become a privilege-escalation path.
- Nested delegation is blocked in tool handlers even though tools are injected consistently to preserve prompt/cache shape.
- Expert matching is normalized/fuzzy only as implemented in `tool.go`; ambiguous matches must not select an arbitrary agent.
- Parent cancellation propagates to children and stops launching queued work.
- Reports are bounded and structured so the parent can reason about status without parsing child event streams.

## Prompt stability

`BeforeModel` always injects the delegation prompts, and `BeforeAgent` injects tool definitions where configured. This keeps forked sessions' prompt prefixes and provider cache keys stable. Do not conditionally reorder these blocks based on runtime results.

## Tests

Run:

```bash
go test ./core/subagents
```

Tests use fake agents and session forkers. Add deterministic tests for semaphore release, cancellation, ordered output, catalog reload, anti-nesting, and partial failures before changing orchestration.
