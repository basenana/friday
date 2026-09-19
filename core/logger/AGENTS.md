# logger — Dependency-light structured logging facade

This package defines Friday's core logging interface and global root logger. Application setup may install an adapter, while core packages can log without importing a concrete logging framework.

## Files

- `interface.go` defines the structured and formatted logging methods.
- `root.go` owns the synchronized global logger and named logger creation.
- `default.go` implements the built-in writer-backed logger.
- `policy_test.go` locks default-output and formatting policy.

## Public API

```go
func New(name string) Logger
func Root() Logger
func SetRoot(l Logger)
```

`Logger` supports `Named`, `With`, plain `Info/Warn/Error`, formatted `Infof/Warnf/Errorf`, and structured `Infow/Warnw/Errorw` methods.

## Behavior

- Before configuration, `Root` lazily creates a default logger writing to `io.Discard`. Libraries therefore remain silent by default.
- `New(name)` derives a named child from the current root.
- `SetRoot` is synchronized and intended for application startup. Replacing it later affects newly acquired loggers, not necessarily children already retained by packages.
- Logging must never be required for correctness.
- Structured keys should be stable, low-cardinality names; do not include secrets or unbounded model payloads.
- Odd structured key/value input follows the default logger's tested policy. Preserve that behavior in adapters.
- Core packages should depend on this interface; root-level `utils/logger` performs concrete startup wiring.

## Tests

Run:

```bash
go test ./core/logger
```

Add tests when changing default silence, field formatting, names, or root replacement semantics.
