# tracing — Pluggable spans with a no-op default

This package provides a small tracing abstraction for core code without binding it to OpenTelemetry or another backend. It includes global tracer installation, context propagation, typed attributes, error/status helpers, and a zero-cost-style no-op implementation.

## Files

- `interface.go` defines `Tracer`, `Span`, status codes, attributes, and span options.
- `context.go` stores and retrieves spans from contexts.
- `noop.go` implements the fallback tracer/span.
- `root.go` owns the global tracer and convenience helpers.
- `tracing_test.go` covers propagation, attributes, errors, truncation, and no-op behavior.

## Core API

```go
type Tracer interface {
    Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
}

type Span interface {
    SetAttributes(attrs ...Attribute)
    AddEvent(name string, attrs ...Attribute)
    SetStatus(code StatusCode, msg string)
    RecordError(err error)
    End()
}

func SetGlobalTracer(t Tracer)
func GlobalTracer() Tracer
func Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
```

## Invariants

- If no tracer is installed, `GlobalTracer` returns a no-op tracer.
- The no-op tracer itself returns the original context, but top-level `tracing.Start` always stores the returned span in a derived context so `SpanFromContext` works consistently.
- Tracer implementations should not apply `SpanOption` attributes themselves; `tracing.Start` applies them once after creation.
- Callers must call `End` exactly once, normally with `defer`.
- `RecordError(nil)` is a no-op.
- `DeferStatus` expects a pointer to the named return error and marks success/error at exit.
- String attributes should use `TruncateAttr` for potentially large values; its byte limit is 2048 plus the truncation marker.
- Attribute values are restricted to string, int64, float64, and bool.
- Global tracer access is synchronized, but changing the tracer during normal execution is discouraged.

## Tests

Run:

```bash
go test ./core/tracing
```

Tests are hermetic. Add propagation and double-application regression tests for any new tracer adapter or option behavior.
