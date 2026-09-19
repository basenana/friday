# daemon — Local WebSocket server exposing session actors

This package serves Friday actors to local clients over a versioned WebSocket protocol. It validates session identity, replays durable events, follows live bus output, and maps client input onto the actor registry.

## Files

- `server.go` defines server configuration, HTTP routing, startup, and graceful shutdown.
- `connection.go` owns one WebSocket connection, replay/live fan-in, input handling, and deduplication.
- `protocol.go` defines protocol version 1 frames, limits, validation, and wire errors.
- `catalog.go` adapts the persistent session manager to daemon session lookup.
- `server_test.go` exercises handshake, replay, live delivery, validation, and shutdown.

## Public API

```go
func NewServer(cfg Config, registry ActorRegistry, catalog SessionCatalog) (*Server, error)
func NewSessionCatalog(manager *sessions.Manager) (SessionCatalog, error)
func (s *Server) Shutdown(ctx context.Context) error
```

`ActorRegistry` and `SessionCatalog` are deliberately narrow interfaces so protocol tests can use fakes. Keep daemon code dependent on these contracts rather than concrete registries.

## Network contract

- The service is local-first and listens on the configured localhost address; the CLI default port is 8999.
- WebSocket clients connect at `/ws` and negotiate protocol version 1.
- Session/thread IDs are validated against the protocol's restricted regular expression before registry or filesystem access.
- Read size, frame size, and write deadlines are bounded. Preserve limits when adding frame types.
- Invalid protocol messages receive stable wire errors and do not reach actor dispatch.
- Shutdown stops accepting connections, closes active clients, and delegates actor shutdown through the registry contract.

## Replay and live delivery

- A connection resolves the requested session through `SessionCatalog` before attaching to an actor.
- Durable actor events are replayed first, then live bus events are forwarded.
- The handoff deduplicates by event identity/sequence so events observed during replay are not emitted twice.
- Replay ordering must remain the sink's durable order.
- Client input is translated to a validated bus envelope and dispatched through `ActorRegistry`; the daemon does not call agent methods directly.
- Connection writers are serialized. Never write concurrently to the same WebSocket.
- Backpressure and connection cancellation must not stall actor execution.

## Protocol changes

Treat `protocol.go` as a public compatibility boundary. Additive frame changes still require validation and tests; incompatible changes require a new protocol version rather than silently changing v1.

## Tests

Run:

```bash
go test ./daemon
```

The tests use `httptest` and bind a loopback port. Sandboxes that prohibit local listening will fail for environmental reasons; run them in a normal host or container network namespace.
