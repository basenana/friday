package tracing

import (
	"context"
	"errors"
	"testing"
)

func TestNoopTracerIsZeroCost(t *testing.T) {
	tracer := nopTracer{}
	ctx := context.Background()

	// Start should return original ctx unchanged (no WithValue call)
	newCtx, span := tracer.Start(ctx, "test")
	if newCtx != ctx {
		t.Error("nopTracer.Start should return original context without modification")
	}

	// Span should be noopSpan
	if _, ok := span.(noopSpan); !ok {
		t.Error("nopTracer.Start should return noopSpan")
	}

	// All span methods should be no-ops (just verify they don't panic)
	span.SetAttributes(String("key", "value"))
	span.AddEvent("event", String("k", "v"))
	span.SetStatus(StatusOK, "")
	span.RecordError(errors.New("test"))
	span.End()
}

func TestNoopSpanEndMultipleTimes(t *testing.T) {
	span := noopSpan{}
	span.End()
	span.End() // should not panic
}

func TestGlobalTracerDefaultIsNoop(t *testing.T) {
	// Reset global state
	SetGlobalTracer(nil)

	tracer := GlobalTracer()
	if _, ok := tracer.(nopTracer); !ok {
		t.Error("default global tracer should be nopTracer")
	}
}

func TestSetGlobalTracer(t *testing.T) {
	// Reset
	SetGlobalTracer(nil)

	mock := &mockTracer{}
	SetGlobalTracer(mock)

	tracer := GlobalTracer()
	if tracer != mock {
		t.Error("GlobalTracer should return the tracer set via SetGlobalTracer")
	}

	// Cleanup
	SetGlobalTracer(nil)
}

func TestStartUsesGlobalTracer(t *testing.T) {
	// Reset
	SetGlobalTracer(nil)

	mock := &mockTracer{}
	SetGlobalTracer(mock)

	ctx := context.Background()
	_, span := Start(ctx, "test.operation")

	if _, ok := span.(*mockSpan); !ok {
		t.Error("Start should use GlobalTracer to create spans")
	}

	// Cleanup
	SetGlobalTracer(nil)
}

func TestContextWithSpan(t *testing.T) {
	mock := &mockTracer{}
	SetGlobalTracer(mock)
	defer SetGlobalTracer(nil)

	ctx := context.Background()
	ctx, span := Start(ctx, "parent")

	retrieved := SpanFromContext(ctx)
	if retrieved != span {
		t.Error("SpanFromContext should return the span stored in context")
	}
}

func TestSpanFromContextEmpty(t *testing.T) {
	ctx := context.Background()
	span := SpanFromContext(ctx)

	if _, ok := span.(noopSpan); !ok {
		t.Error("SpanFromContext with no span should return noopSpan")
	}
}

func TestSpanParentChild(t *testing.T) {
	mock := &mockTracer{}
	SetGlobalTracer(mock)
	defer SetGlobalTracer(nil)

	ctx := context.Background()

	ctx, parent := Start(ctx, "parent")
	if SpanFromContext(ctx) != parent {
		t.Error("parent span should be retrievable from context")
	}

	childCtx, child := Start(ctx, "child")
	if SpanFromContext(childCtx) != child {
		t.Error("child span should be retrievable from context")
	}

	// The mock tracer should have been called twice
	if mock.startCount != 2 {
		t.Errorf("expected 2 Start calls, got %d", mock.startCount)
	}

	// Verify spans are different instances
	if parent == child {
		t.Error("parent and child spans should be different instances")
	}
}

func TestAttributeConstructors(t *testing.T) {
	tests := []struct {
		name     string
		attr     Attribute
		wantKey  string
		wantVal  any
	}{
		{"string", String("k", "v"), "k", "v"},
		{"int", Int("k", 42), "k", int64(42)},
		{"float", Float("k", 3.14), "k", 3.14},
		{"bool", Bool("k", true), "k", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.attr.Key != tt.wantKey {
				t.Errorf("key = %q, want %q", tt.attr.Key, tt.wantKey)
			}
			if tt.attr.Value != tt.wantVal {
				t.Errorf("value = %v, want %v", tt.attr.Value, tt.wantVal)
			}
		})
	}
}

func TestWithAttributes(t *testing.T) {
	cfg := applySpanOptions([]SpanOption{
		WithAttributes(String("a", "1"), Int("b", 2)),
	})
	if len(cfg.attributes) != 2 {
		t.Fatalf("expected 2 attributes, got %d", len(cfg.attributes))
	}
	if cfg.attributes[0].Key != "a" || cfg.attributes[1].Key != "b" {
		t.Error("attributes not applied correctly")
	}
}

func TestApplySpanOptionsEmpty(t *testing.T) {
	cfg := applySpanOptions(nil)
	if len(cfg.attributes) != 0 {
		t.Error("empty options should produce zero attributes")
	}
}

func TestStartAppliesAttributesToSpan(t *testing.T) {
	mock := &mockTracer{}
	SetGlobalTracer(mock)
	defer SetGlobalTracer(nil)

	ctx := context.Background()
	_, span := Start(ctx, "test.op",
		WithAttributes(String("k1", "v1"), Int("k2", 42)),
	)

	ms, ok := span.(*mockSpan)
	if !ok {
		t.Fatal("expected *mockSpan")
	}

	// mockTracer.Start does NOT parse opts (per contract),
	// so all attributes come from Start()'s SetAttributes call.
	if len(ms.attrs) != 2 {
		t.Fatalf("expected 2 attributes, got %d", len(ms.attrs))
	}
	if ms.attrs[0].Key != "k1" || ms.attrs[0].Value != "v1" {
		t.Errorf("attr[0] = %v, want {k1, v1}", ms.attrs[0])
	}
	if ms.attrs[1].Key != "k2" || ms.attrs[1].Value != int64(42) {
		t.Errorf("attr[1] = %v, want {k2, 42}", ms.attrs[1])
	}
}

func TestStartNoAttributesSkipsSetAttributes(t *testing.T) {
	mock := &mockTracer{}
	SetGlobalTracer(mock)
	defer SetGlobalTracer(nil)

	ctx := context.Background()
	_, span := Start(ctx, "test.no-attrs")

	ms := span.(*mockSpan)
	if len(ms.attrs) != 0 {
		t.Errorf("expected 0 attributes, got %d", len(ms.attrs))
	}
}

func TestIntValConstructor(t *testing.T) {
	attr := IntVal("count", 7)
	if attr.Key != "count" {
		t.Errorf("key = %q, want %q", attr.Key, "count")
	}
	if attr.Value != int64(7) {
		t.Errorf("value = %v (type %T), want int64(7)", attr.Value, attr.Value)
	}
}

func TestSpanFromContextAfterStartWithNoop(t *testing.T) {
	// With noop tracer, Start() still stores span in context
	SetGlobalTracer(nil)

	ctx := context.Background()
	ctx, _ = Start(ctx, "test")

	span := SpanFromContext(ctx)
	if _, ok := span.(noopSpan); !ok {
		t.Error("SpanFromContext should return noopSpan when using noop tracer")
	}
}

func TestDeferStatusWithNilError(t *testing.T) {
	mock := &mockTracer{}
	SetGlobalTracer(mock)
	defer SetGlobalTracer(nil)

	ctx := context.Background()
	_, span := Start(ctx, "test.ok")
	ms := span.(*mockSpan)

	var err error
	DeferStatus(span, &err)

	if ms.status != StatusOK {
		t.Errorf("expected StatusOK, got %v", ms.status)
	}
	if ms.err != nil {
		t.Errorf("expected nil error, got %v", ms.err)
	}
}

func TestDeferStatusWithError(t *testing.T) {
	mock := &mockTracer{}
	SetGlobalTracer(mock)
	defer SetGlobalTracer(nil)

	ctx := context.Background()
	_, span := Start(ctx, "test.err")
	ms := span.(*mockSpan)

	err := errors.New("something failed")
	DeferStatus(span, &err)

	if ms.status != StatusError {
		t.Errorf("expected StatusError, got %v", ms.status)
	}
	if ms.err == nil || ms.err.Error() != "something failed" {
		t.Errorf("expected recorded error, got %v", ms.err)
	}
}

func TestDeferStatusWithNoopSpan(t *testing.T) {
	SetGlobalTracer(nil)

	ctx := context.Background()
	_, span := Start(ctx, "test.noop")

	// Should not panic with noop span
	var err error
	DeferStatus(span, &err)

	err = errors.New("fail")
	DeferStatus(span, &err)
}

// Mock implementation for testing

type mockTracer struct {
	startCount int
	lastCtx    context.Context
}

func (m *mockTracer) Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	m.startCount++
	span := &mockSpan{name: name, tracer: m}
	// Per Tracer contract: do NOT parse opts here.
	// tracing.Start() applies WithAttributes automatically.
	newCtx := ContextWithSpan(ctx, span)
	m.lastCtx = newCtx
	return newCtx, span
}

type mockSpan struct {
	name   string
	tracer *mockTracer
	attrs  []Attribute
	events []string
	status StatusCode
	err    error
	ended  bool
}

func (s *mockSpan) SetAttributes(attrs ...Attribute) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *mockSpan) AddEvent(name string, attrs ...Attribute) {
	s.events = append(s.events, name)
}

func (s *mockSpan) SetStatus(code StatusCode, msg string) {
	s.status = code
}

func (s *mockSpan) RecordError(err error) {
	if err != nil {
		s.err = err
		s.status = StatusError
	}
}

func (s *mockSpan) End() {
	s.ended = true
}
