# actor — AG-UI-compatible asynchronous actor runtime

This package wraps a core agent and session in a long-lived actor with queued turns, preemption, durable ordered events, lossy live subscriptions, cards, forms, and tool-event translation. Root-level `actor` owns process-wide registry and bridge lifecycle.

## Root files

- `actor.go` defines `Actor`, message handling, turns, forms, subscriptions, and shutdown.
- `options.go` defines buffers, sinks, attachment policy, tools, and lifecycle options.
- `inbox.go` implements ordinary and preempt queues plus merge behavior.
- `stream.go` implements bounded live subscriptions.
- `translator.go` converts core deltas/events into actor events.
- `emit.go` emits cards and custom events.
- `lifecycle.go` tracks actor/turn lifecycle events.
- `terminal.go` classifies terminal events.
- `tool_plan.go` implements planning/form tools.
- `tool_show_card.go` implements rich-card emission tools.
- `path_policy.go` validates artifact paths.
- `id.go` creates event/run/item IDs.

## Subpackages

- `events/types.go` defines the event envelope and kinds.
- `events/text.go` defines text-message payloads.
- `events/tool.go` defines tool-call and result payloads.
- `events/step.go` defines step lifecycle payloads.
- `events/state.go` defines state snapshot payloads.
- `events/lifecycle.go` defines run lifecycle payloads.
- `events/custom.go` defines custom event payloads.
- `events/encoding.go` handles wire encoding.
- `cards/types.go` defines card envelopes and components.
- `cards/catalog.go` validates supported card kinds.
- `cards/diff.go` defines diff cards.
- `cards/file.go` defines file cards.
- `cards/form.go` defines interactive form cards.
- `cards/html_preview.go` defines HTML preview cards.
- `cards/image.go` defines image cards.
- `cards/mermaid.go` defines Mermaid cards.
- `cards/spreadsheet.go` defines spreadsheet cards.
- `cards/table.go` defines table cards.
- `sink/sink.go` defines durable append/replay contracts.
- `sink/jsonl.go` implements bounded JSONL persistence.

## Key API

```go
func New(agent agents.Agent, sess *session.Session, opts ...Option) *Actor
func (a *Actor) Send(ctx context.Context, msg Message) error
func (a *Actor) SendPreempt(ctx context.Context, reason string) error
func (a *Actor) SendPreemptScope(ctx context.Context, reason string, scope PreemptScope) error
func (a *Actor) Subscribe() *Subscription
func (a *Actor) SubmitForm(formID string, values map[string]any) error
func (a *Actor) WaitForForm(ctx context.Context, formID string) (FormOutcome, error)
func (a *Actor) EmitCard(kind, title string, component map[string]any) (string, error)
func (a *Actor) EmitCustom(name, itemID string, payload any)
```

## Event durability and streaming

- Publication is serialized by the actor's publish lock.
- For each event, assign its sequence, append to the configured sink, and only then dispatch to live subscribers.
- Never publish a live event that failed durable append; replay and live order must agree.
- Event sequences are monotonic within one actor stream.
- Live subscriptions are bounded and intentionally lossy for slow consumers, but terminal/lifecycle events are preserved according to `stream.go` policy.
- Consumers needing complete history must replay the sink and then follow live events with sequence deduplication.
- Closing a subscription is idempotent and must not close the actor or sibling subscriptions.

## Inbox and turns

- Ordinary messages are queued separately from preemption.
- Compatible pending messages may merge; preserve message identity and metadata rules when changing coalescing.
- Preemption can target the current turn or a broader scope and must cancel model/tool work promptly.
- Only one ordinary actor turn executes at a time.
- Shutdown rejects new input, resolves/cancels waiting forms, waits for owned work, emits terminal state, and closes stream/sink in defined order.
- Stop reasons are explicit protocol data; do not infer them only from display text.

## Forms, cards, and paths

- Form IDs associate one request with one eventual submission/cancellation outcome.
- Duplicate or unknown form submissions fail rather than waking an unrelated waiter.
- Card payloads pass through the catalog validators before publication.
- Artifact cards use the path policy: resolve within approved roots, reject traversal/symlink escapes, and keep user-facing references stable.
- Custom events still require serializable payloads and stable names.

## Tests

Run:

```bash
go test ./core/actor/...
```

Tests are in-process and hermetic. Concurrency changes should also run with `-race`. Preserve regression coverage for persist-before-live ordering, preemption, merge rules, terminal delivery, form cancellation, path policy, and bounded JSONL compaction.
