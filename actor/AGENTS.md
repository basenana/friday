# actor — Per-session actor registry and lifecycle management

This package owns the process-level registry that turns persistent Friday sessions into live `core/actor.Actor` instances. CLI, TUI, and daemon entry points use it to share actors safely, dispatch input, and reclaim idle sessions.

## Files

- `registry.go` defines `Registry`, `RegistryConfig`, actor construction, dispatch, leases, idle eviction, and shutdown.
- `managed.go` holds one live actor and its input/output bridges plus activity accounting.
- `paths.go` derives actor event and attachment paths under the configured data directory.
- `registry_test.go` covers concurrent construction, lifecycle leases, eviction, dispatch, and shutdown.
- `bus_test.go` covers registry integration with the event bus.

## Public API

```go
func NewRegistry(sessMgr setup.SessionManager, appCfg *config.Config, cfg RegistryConfig) (*Registry, error)
func (r *Registry) GetOrCreate(sessionID string) (*coreactor.Actor, error)
func (r *Registry) AcquireLifecycle(sessionID string) (sessions.SessionLifecycle, func(), error)
func (r *Registry) DispatchInput(env bus.Envelope) error
```

`RegistryConfig` supplies the data directory, bus, idle timeout, sweep interval, and actor options. Keep defaults and path derivation centralized here rather than in callers.

## Lifecycle and concurrency

- `GetOrCreate` is single-flight per session. Concurrent callers must observe one constructed actor, not duplicate runtimes.
- Construction delegates agent assembly to `setup.NewAgentWithLifecycle`; do not reproduce setup wiring in this package.
- A managed actor tracks active turns, acquired lifecycle leases, and background tasks. Idle eviction must not close an actor while any of those are active.
- `AcquireLifecycle` returns both the lifecycle object and a release function. Every successful acquisition must release exactly once.
- Input dispatch validates and routes a bus envelope to the actor identified by its session metadata.
- Activity timestamps are updated by real work, not merely by registry lookup.
- Shutdown ordering is deliberate: close the input bridge, then the actor, then the output bridge, then the agent context. Changing the order can lose events or leave producers writing to closed consumers.
- Registry shutdown is idempotent and prevents later construction.

## Paths and persistence

Actor event logs and attachments are session-scoped beneath the configured Friday data directory. Use helpers in `paths.go`; do not concatenate user-supplied session IDs into paths without the existing validation.

The registry does not replace session persistence. Session metadata remains owned by `sessions`, while actor event persistence belongs to `core/actor` sinks.

## Tests

Run:

```bash
go test ./actor
```

Tests are in-process and use temporary directories. Concurrency changes should be checked with `go test -race ./actor` where the environment supports the race detector.
