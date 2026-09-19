# state — Scoped key-value state interface and in-memory implementation

This package provides a minimal state abstraction for app- and user-scoped string values. It is independent of session history and is suitable for lightweight extension state.

## Files

- `interface.go` defines scopes and the `State` interface.
- `inmemory.go` implements synchronized process-local state.

## Public API

```go
type State interface {
    Get(ctx context.Context, scope StateScope, key string) (string, error)
    Set(ctx context.Context, scope StateScope, key string, value string) error
    Delete(ctx context.Context, scope StateScope, key string) error
    List(ctx context.Context, scope StateScope) ([]string, error)
    WithUser(userID string) State
}

func NewInMemory() State
```

Valid scopes are `ScopeApp` and `ScopeUser`.

## Invariants

- `WithUser` returns a view bound to one user while sharing the same underlying store.
- User-scoped operations require a user identity and must not collide across users.
- App-scoped values are shared by all user views.
- Implementations must be safe for concurrent access.
- Context cancellation should be honored by persistent implementations even though the in-memory backend completes quickly.
- `List` returns keys for exactly one scope/view; it must not leak another user's keys.
- Missing-key behavior must stay consistent across implementations.
- This interface stores strings deliberately. Serialization policy belongs to callers.

## Tests

There are currently no package tests. Any behavioral change should first add tests for scope isolation, user views, delete/missing behavior, deterministic listing expectations, and concurrent access:

```bash
go test ./core/state
```
