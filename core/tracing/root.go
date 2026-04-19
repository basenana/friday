package tracing

import (
	"context"
	"sync"
)

var (
	globalTracer   Tracer
	globalTracerMu sync.RWMutex
)

// SetGlobalTracer replaces the global tracer.
// Typically called once at application startup by the user to inject
// their tracing implementation (e.g., OpenTelemetry, database recorder).
func SetGlobalTracer(t Tracer) {
	globalTracerMu.Lock()
	defer globalTracerMu.Unlock()
	globalTracer = t
}

// GlobalTracer returns the global tracer. If none has been set,
// returns a no-op tracer (zero overhead).
func GlobalTracer() Tracer {
	globalTracerMu.RLock()
	defer globalTracerMu.RUnlock()
	if globalTracer == nil {
		return nopTracer{}
	}
	return globalTracer
}

// DeferStatus is a helper for the common defer pattern that records error
// status on a span. Use with named return values:
//
//	func foo() (retErr error) {
//	    ctx, span := tracing.Start(ctx, "op")
//	    defer span.End()
//	    defer tracing.DeferStatus(span, &retErr)
//	    ...
//	}
func DeferStatus(span Span, errPtr *error) {
	if *errPtr != nil {
		span.RecordError(*errPtr)
	} else {
		span.SetStatus(StatusOK, "")
	}
}

// maxSpanAttrLen is the maximum byte length for span attribute string values.
// Most tracing backends (OTLP, Jaeger) cap attribute values around 4KB;
// we use 2048 to stay well within that limit.
const maxSpanAttrLen = 2048

// TruncateAttr returns a String attribute whose value is capped at maxSpanAttrLen
// bytes. If the value is within the limit it is returned unchanged; otherwise a
// truncation marker is appended so observers know the value was cut.
func TruncateAttr(key, value string) Attribute {
	if len(value) <= maxSpanAttrLen {
		return String(key, value)
	}
	return String(key, value[:maxSpanAttrLen]+"...[truncated]")
}

// Start is a convenience function that calls GlobalTracer().Start().
// This is the primary entry point for instrumentation code:
//
//	ctx, span := tracing.Start(ctx, "agent.react.chat")
//	defer span.End()
func Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	ctx, span := GlobalTracer().Start(ctx, name, opts...)
	// Ensure span is always retrievable via SpanFromContext,
	// regardless of whether the Tracer implementation stores it.
	ctx = ContextWithSpan(ctx, span)
	if cfg := applySpanOptions(opts); len(cfg.attributes) > 0 {
		span.SetAttributes(cfg.attributes...)
	}
	return ctx, span
}
