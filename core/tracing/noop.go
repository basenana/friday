package tracing

import "context"

// nopTracer is the default tracer. All operations are no-ops.
type nopTracer struct{}

func (nopTracer) Start(ctx context.Context, _ string, _ ...SpanOption) (context.Context, Span) {
	// Return original ctx unchanged — no context.WithValue, zero allocation.
	return ctx, noopSpan{}
}

// noopSpan discards all data. Zero-size struct, never allocates.
type noopSpan struct{}

func (noopSpan) SetAttributes(_ ...Attribute) {}
func (noopSpan) AddEvent(_ string, _ ...Attribute) {}
func (noopSpan) SetStatus(_ StatusCode, _ string)   {}
func (noopSpan) RecordError(_ error)                {}
func (noopSpan) End()                                {}
