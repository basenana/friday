# contextmgr — Context projection, compaction, refocus, and session memory

This package keeps model requests within provider context limits while retaining recent work and durable session intent. It runs as hooks around agent/model execution and stores its coordination state in `core/session.ContextState`.

## Files

- `manager.go` computes budgets, projects history, truncates large tool results, and triggers soft/hard compaction.
- `decision_compact.go` implements optional DecisionProvider-guided micro-compaction. It pairs only old tool calls whose matching results also live in the old slice, evaluates at most 50 pairs per request, starts all batches concurrently, and leaves QPM enforcement to the shared DecisionProvider client. Each batch has a 30 s deadline and sends two `Noul` questions per pair (call+result), with goals capped to the last three plain-text user messages (500 runes each). Failed batches and malformed answers conservatively keep affected pairs; parent cancellation aborts and waits for every batch.
- `decision_cache.go` owns the private `decisioncache` session-record protocol. The record stores only the current user epoch and validated `keep`, `drop_result`, or `drop_call` actions keyed by tool-call ID plus a pair hash. The epoch hashes the session ID, internal cache version, and normalized history through the latest user message; the pair hash includes the full local call/result while the record stores neither tool arguments nor result bodies. A new epoch replaces the record, and cache I/O failure only loses the optimization.
- `refocus.go` implements explicit objective refocusing from a user-supplied structured summary.
- `session_memory.go` generates and persists rolling session memory.
- Tests cover threshold boundaries, projection, rollback, refocus validation, and memory scheduling.

## Public constructors

```go
func New(llm providers.Client, cfg Config) *Manager
func NewRefocusHook() *Refocus
func (r *Refocus) BeforeAgent(_ context.Context, sess *session.Session, req session.AgentRequest) error
```

Important defaults from `manager.go`:

- context window: 128,000 tokens when the provider/config gives none;
- soft threshold: 0.70;
- hard threshold: 0.85;
- maximum projected tool-result text: 600 characters;
- session-memory threshold: 15,000 tokens.

## Budget and compaction behavior

- Budget calculations include stable reserved tokens injected after projection, such as an approved plan.
- Below the soft threshold, preserve history and avoid unnecessary summaries.
- Above the soft threshold, micro-compaction trims oversized tool results and projects a smaller request. When `Config.DecisionProvider` is set, the soft path consults the provider for paired old call/result messages and applies the projection only when it saves at least 20%. Pair collection is bounded by the old-message slice (the last four conversation groups are always untouched), boundary pairs that straddle the old/tail split are conservatively kept, and failed or malformed decisions keep only the affected pairs. Valid decisions are persisted even when the 20% gate rejects the projection, so a retry in the same user epoch is deterministic and provider-free. Once a useful prefix is frozen, that prefix bypasses both cache reads and provider calls.
- Above the hard threshold, hard compaction summarizes older groups while preserving recent groups and leading context.
- Projection affects the provider request; durable history changes only through explicit successful compaction.
- Failed summarization must not destroy or partially replace the current history.
- Prompt cache keys are enabled only after useful history growth to avoid churn on tiny conversations.
- Token estimation and provider checkpoints come from `core/session`; do not maintain an independent count.

## Refocus

- Explicit refocus is a no-op below roughly 30,000 tokens.
- The submitted summary is limited to 8,000 characters and must follow the required objective/progress/facts/next-steps structure.
- Successful refocus replaces stale history with the summary plus recent messages and keeps session state internally consistent.
- Invalid summaries fail without mutating history.

## Session memory

- Session memory generation is asynchronous and threshold-driven so the active turn is not blocked by persistence work.
- Root-session identity determines the durable memory record; forks do not create unrelated competing memories.
- Store failures are logged/contained according to the implementation and must not corrupt chat history.

## Tests

Run:

```bash
go test ./core/contextmgr
```

Tests are hermetic with fake providers/stores. Threshold changes require boundary tests for both ratios, reserved-token accounting, cancellation, and rollback.
