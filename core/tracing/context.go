package tracing

import "context"

// spanContextKey is an unexported key type to prevent context collisions.
type spanContextKey struct{}

// ContextWithSpan returns a new context carrying the given span.
func ContextWithSpan(ctx context.Context, span Span) context.Context {
	return context.WithValue(ctx, spanContextKey{}, span)
}

// SpanFromContext returns the span stored in ctx, or a no-op span if none.
func SpanFromContext(ctx context.Context) Span {
	if span, ok := ctx.Value(spanContextKey{}).(Span); ok {
		return span
	}
	return noopSpan{}
}
