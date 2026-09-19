# session — In-memory conversation state, hooks, events, and compaction

This package is the core session model used by every agent. It owns synchronized message history, token estimates, hook registration, event publication, forks, context state, and low-level compaction helpers. Root-level `sessions` adds disk persistence and lifecycle management.

## Files

- `session.go` defines `Session`, options, history mutation, events, tokens, and forks.
- `hooks.go` defines agent/model hook contracts and ordered registration.
- `compact.go` implements history compaction and summary replacement.
- `compact_events.go` publishes compaction lifecycle events.
- `context_state.go` stores prompt budgets, projections, pending summaries, and session memory.
- `records.go` defines durable/session records and metadata conversion.
- `token_calibrate.go` calibrates token estimates from provider usage.

## Key API

```go
func New(id string, llm providers.Client, options ...Option) *Session
func (s *Session) Fork() *Session
func (s *Session) HistoryLen() int
func (s *Session) PublishEvent(evt types.Event)
func (s *Session) ReplaceHistory(msgs ...types.Message) error
```

Hook interfaces in `hooks.go` are extension points used by context management, skills, subagents, planning, and setup. Keep hook methods small and cancellation-aware.

## History invariants

- Session history is synchronized; use its methods rather than retaining and mutating internal slices.
- Tool calls and tool results must form valid pairs. `ReplaceHistory` validates the replacement instead of accepting structurally invalid transcripts.
- `Fork` copies the usable conversation state but trims orphan tool calls/results at the boundary.
- Forks isolate later history mutation while sharing only explicitly designed capabilities.
- Token counts track effective history and are recalibrated from provider checkpoints where available.
- Compaction preserves the leading system context and a recent tail while replacing older material with a summary.
- A failed compaction must leave the previous valid history usable.

## Events and context state

- Root session events receive monotonically increasing sequence numbers.
- Forked activity is associated with the root event stream where configured; do not create competing sequence generators.
- Event subscribers must not be allowed to block core session mutation indefinitely.
- `ContextState` is the shared contract between session, `contextmgr`, and request projection. Update it under the session's synchronization rules.
- Pending summaries and projections are transactional state: only commit them after the corresponding history change succeeds.

## Relationships

`core/promptcontext` provides request-local leading context blocks and is consumed around session/provider request construction. It is intentionally a small adjacent package rather than session-global state.

The root `sessions` package persists records and wraps a `Session` in lifecycle handles. Do not add filesystem policy to this core package.

## Tests

Run:

```bash
go test ./core/session
```

Tests are hermetic. Extend them for invalid tool pairing, fork boundaries, event sequence behavior, token calibration, and rollback on compaction failure.
