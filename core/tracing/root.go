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

// Start is a convenience function that calls GlobalTracer().Start().
// This is the primary entry point for instrumentation code:
//
//	ctx, span := tracing.Start(ctx, "agent.react.chat")
//	defer span.End()
func Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	return GlobalTracer().Start(ctx, name, opts...)
}
