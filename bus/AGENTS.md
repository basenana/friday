# bus — Ordered session event routing and actor bridges

This package defines Friday's process-local event vocabulary, stable topic names, ordered subscriptions, and bridges between the shared event bus and `core/actor` runtimes.

## Files

- `envelope.go` defines the transport envelope and its validation/normalization helpers.
- `topics.go` defines stable session topic constructors and event payload types.
- `feed.go` exposes a serial, bounded event feed for TUI and daemon consumers.
- `bridge/in.go` converts input topics into actor inbox messages.
- `bridge/out.go` publishes actor events back to session topics.
- `bridge/routing.go` contains message and control-route selection.
- `bridge/tools.go` converts tool and card payloads used by the bridges.
- `topics_test.go` locks topic and payload contracts.

## Key API

```go
func NewFeed(b *eventbus.Bus, cfg eventbus.SerialConfig, patterns ...string) *Feed
func (f *Feed) Events() <-chan events.Event
func (f *Feed) Done() <-chan struct{}
func (f *Feed) Close()
```

Topic helpers in `topics.go` are part of the compatibility surface. Use them instead of constructing topic strings at call sites.

## Ordering and delivery

- `Feed` uses the core event bus serial subscriber so events matching multiple topics retain one strict delivery order.
- Its public event channel is buffered at 512 entries. The serial subscription queue applies the caller's overflow policy; `SubscribeAgentFeed` explicitly uses a 512-entry drop-oldest queue and accounts for dropped events.
- `Close` stops the subscription and closes `Done`.
- The `Events` channel is intentionally never closed. Consumers must select on `Done` rather than ranging until channel closure.
- A feed may be closed more than once.
- Topic names include a normalized session identifier and separate input, output, lifecycle, and control traffic.

## Bridge invariants

- `InBridge` subscribes to the session's input routes and forwards normalized messages to the actor inbox.
- Preemption/control input has a separate route from ordinary queued user input; do not collapse the two paths.
- `OutBridge` observes actor events and publishes envelopes with the same session identity.
- Bridge shutdown must unsubscribe before discarding references to the actor or bus.
- Envelope metadata is transport data. Do not hide durable actor state exclusively inside bus metadata.
- Bridge conversion should preserve IDs used by replay and client-side deduplication.

## Relationships

- `actor.Registry` owns bridge construction and closure.
- `core/actor` owns execution and durable event sequencing.
- `daemon` and `tui` consume `Feed` rather than subscribing independently with weaker ordering guarantees.

## Tests

Run:

```bash
go test ./bus ./bus/bridge
```

Add contract tests whenever changing topic text, envelope fields, routing precedence, buffer behavior, or close semantics.
