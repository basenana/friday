# utils — Legacy shared helpers plus root-module logging and signal utilities

This directory contains small root-module helpers (`events`, JSON extraction,
hashing, strings), the zap-based application logging adapter in `utils/logger`,
and process signal helpers in `utils/signal`. The root `utils` package and
`utils/signal` currently have no in-repository importers; do not add new usage
without checking whether standard library or a focused package is better.
`utils/logger` remains used by CLI/session bootstrap, while new runtime code
should depend on `core/logger` per the repository logging guidance.

## Files

| File | Responsibility |
|---|---|
| `events.go` | Deprecated process-global eventbus aliases (`Subscribe`, `Publish`, etc.); prefer an explicit `eventbus.Bus` |
| `fmt.go` | `ExtractJSON`: extract JSON from a fenced block or first `{` through last `}` |
| `hash.go` | FNV-32a/deep-hash helpers (`ComputeHierarchyStructHash`, `ComputeStructHash`, `DeepHashObject`) |
| `strings.go` | Pretty JSON (`Res2Str`), rune-safe truncation (`CutToSafeLength`), grep-with-context (`GrepC`) |
| `logger/logger.go` | Zap initialization (file or stdout), named loggers, sync/close, file-backed status |
| `logger/adapter.go` | Adapts zap to `core/logger.Logger` (`Named`, `With`, level methods) |
| `signal/context.go` | Terminal signal context/channel helpers and programmatic shutdown request |
| `signal/lifecycle.go` | Lifecycle hooks for SIGUSR1/SIGUSR2 and shutdown coordination |

## Key API

```go
func ExtractJSON(response string) string
func ComputeHierarchyStructHash(parent, child interface{}, collisionCount *int32) string
func ComputeStructHash(template interface{}, collisionCount *int32) string
func DeepHashObject(hasher hash.Hash, objectToWrite interface{})
func Res2Str(obj interface{}) string
func CutToSafeLength(content string, safeLen int) string
func GrepC(content string, C int, keywords ...string) string
```

```go
// utils/logger
func Init()
func InitWithFile(logPath string)
func New(name string) *zap.SugaredLogger
func Sync()
func Close()
func IsFileBacked() bool
func CoreLogger() *coreLoggerAdapter

// utils/signal
func HandleTerminalContext() context.Context
func HandleTerminalSignal() chan struct{}
func RequestShutdown()
```

## Behavior invariants and cautions

- `events.go` is deprecated process-global state; actor/TUI/daemon code uses explicit bus instances instead.
- `ExtractJSON` is permissive, not a validator: it strips a ```json fence when present, otherwise slices from the first `{` to the last `}`.
- Hash helpers follow the Kubernetes-style FNV/go-spew pattern; `collisionCount` perturbs the hash for collision retries.
- `CutToSafeLength` truncates by runes (not bytes) and adds a truncation header; `GrepC` returns matching lines with C lines of context.
- `utils/logger.Init()` configures stdout JSON logging, but TUI-reachable runtime code must not call it. Normal CLI startup uses `InitWithFile`.
- If the log file cannot be opened, the logger silently discards output — it must never fall back to terminal output and corrupt the TUI.
- New code should create named loggers through `github.com/basenana/friday/core/logger`; `utils/logger` is the application-level zap wiring/adapter retained for existing bootstrap consumers (`cmd/main.go`, `cmd/root.go`, `sessions/manager.go`).
- `HandleTerminalSignal` exits with status 2 on the second Ctrl-C. Signal channels are buffered so shutdown is not lost while a lifecycle hook is running.
- The root helper package and `utils/signal` currently have no callers. Treat them as legacy/deletion candidates, not patterns to expand; this document does not authorize deleting them.

## Tests

- `fmt_test.go` covers JSON extraction.
- `logger/logger_test.go` covers logger initialization and file-backed behavior.
- `hash.go`, `strings.go`, `events.go`, and `utils/signal` currently have no tests.

No test requires network, ports, or git.
