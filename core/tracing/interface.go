package tracing

import "context"

// StatusCode represents the completion status of a span.
type StatusCode int

const (
	StatusUnset StatusCode = iota
	StatusOK
	StatusError
)

// Attribute is a key-value pair attached to a span.
// Value must be one of: string, int64, float64, bool.
type Attribute struct {
	Key   string
	Value any
}

// Attribute constructors with type safety at call sites.
func String(key, value string) Attribute   { return Attribute{Key: key, Value: value} }
func Int(key string, value int64) Attribute     { return Attribute{Key: key, Value: value} }
func IntVal(key string, value int) Attribute    { return Attribute{Key: key, Value: int64(value)} }
func Float(key string, value float64) Attribute { return Attribute{Key: key, Value: value} }
func Bool(key string, value bool) Attribute     { return Attribute{Key: key, Value: value} }

// SpanOption configures span creation.
type SpanOption func(*spanConfig)

type spanConfig struct {
	attributes []Attribute
}

func applySpanOptions(opts []SpanOption) spanConfig {
	var cfg spanConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithAttributes returns a SpanOption that sets initial attributes on the span.
func WithAttributes(attrs ...Attribute) SpanOption {
	return func(cfg *spanConfig) {
		cfg.attributes = append(cfg.attributes, attrs...)
	}
}

// Tracer creates spans. Implementations must propagate spans via context.
type Tracer interface {
	// Start creates a new span. The returned context carries the span
	// so subsequent Start calls discover their parent.
	//
	// Note: Implementations should NOT extract attributes from opts.
	// The tracing.Start() convenience function applies WithAttributes
	// to the span automatically after creation. Parsing opts in the
	// Tracer would cause attributes to be set twice.
	Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
}

// Span represents a unit of work with start/end, attributes, events, and status.
type Span interface {
	// SetAttributes adds key-value metadata to the span.
	SetAttributes(attrs ...Attribute)

	// AddEvent records a timestamped event within the span.
	AddEvent(name string, attrs ...Attribute)

	// SetStatus sets the span's completion status.
	// msg is only meaningful when code is StatusError.
	SetStatus(code StatusCode, msg string)

	// RecordError adds an error event and sets StatusError.
	// No-op if err is nil.
	RecordError(err error)

	// End completes the span. Must be called exactly once.
	End()
}
