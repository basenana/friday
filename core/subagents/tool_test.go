package subagents

import (
	"context"
	"strings"
	"testing"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
)

// recordingSpan captures SetAttributes calls for inspection.
type recordingSpan struct {
	tracing.Span
	attrs []tracing.Attribute
}

func (s *recordingSpan) SetAttributes(attrs ...tracing.Attribute) {
	s.attrs = append(s.attrs, attrs...)
}

func (s *recordingSpan) End()                              {}
func (s *recordingSpan) SetStatus(_ tracing.StatusCode, _ string) {}

func (s *recordingSpan) attrValue(key string) (string, bool) {
	for _, a := range s.attrs {
		if a.Key == key {
			v, ok := a.Value.(string)
			return v, ok
		}
	}
	return "", false
}

// recordingTracer injects a recordingSpan so tests can inspect attributes.
type recordingTracer struct {
	span *recordingSpan
}

func (r *recordingTracer) Start(ctx context.Context, _ string, opts ...tracing.SpanOption) (context.Context, tracing.Span) {
	r.span = &recordingSpan{Span: tracing.SpanFromContext(ctx)}
	return ctx, r.span
}

// fakeAgent implements agents.Agent for testing.
type fakeAgent struct {
	response string
}

func (f *fakeAgent) Chat(_ context.Context, _ *api.Request) *api.Response {
	resp := api.NewResponse()
	go func() {
		api.SendDelta(resp, types.Delta{Content: f.response})
		resp.Close()
	}()
	return resp
}

func newTestSession(t *testing.T) *session.Session {
	t.Helper()
	return session.New("test-sess", nil)
}

func TestCallSubagentToolTruncatesLongInput(t *testing.T) {
	rec := &recordingTracer{}
	tracing.SetGlobalTracer(rec)
	defer tracing.SetGlobalTracer(nil)

	longInput := strings.Repeat("a", 3000)
	agents := []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: "done"}}}
	handler := callSubagentTool(agents, newTestSession(t), nil)

	req := &tools.Request{
		Arguments: map[string]interface{}{
			"agent_name":    "worker",
			"task_describe": longInput,
		},
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, ok := rec.span.attrValue("subagent.input")
	if !ok {
		t.Fatal("subagent.input attribute not set")
	}
	if len(val) >= 3000 {
		t.Errorf("expected input to be truncated, got len=%d", len(val))
	}
	if !strings.HasSuffix(val, "...[truncated]") {
		t.Errorf("expected truncation marker, got: %q", val[max(0, len(val)-20):])
	}
}

func TestCallSubagentToolTruncatesLongOutput(t *testing.T) {
	rec := &recordingTracer{}
	tracing.SetGlobalTracer(rec)
	defer tracing.SetGlobalTracer(nil)

	longOutput := strings.Repeat("b", 3000)
	agents := []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: longOutput}}}
	handler := callSubagentTool(agents, newTestSession(t), nil)

	req := &tools.Request{
		Arguments: map[string]interface{}{
			"agent_name":    "worker",
			"task_describe": "short task",
		},
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, ok := rec.span.attrValue("subagent.output")
	if !ok {
		t.Fatal("subagent.output attribute not set")
	}
	if len(val) >= 3000 {
		t.Errorf("expected output to be truncated, got len=%d", len(val))
	}
	if !strings.HasSuffix(val, "...[truncated]") {
		t.Errorf("expected truncation marker, got: %q", val[max(0, len(val)-20):])
	}
}

func TestCallSubagentToolShortValuesUnchanged(t *testing.T) {
	rec := &recordingTracer{}
	tracing.SetGlobalTracer(rec)
	defer tracing.SetGlobalTracer(nil)

	agents := []ExpertAgent{{Name: "worker", Agent: &fakeAgent{response: "short output"}}}
	handler := callSubagentTool(agents, newTestSession(t), nil)

	req := &tools.Request{
		Arguments: map[string]interface{}{
			"agent_name":    "worker",
			"task_describe": "short input",
		},
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val, ok := rec.span.attrValue("subagent.input"); ok {
		if strings.Contains(val, "[truncated]") {
			t.Errorf("short input should not be truncated, got: %q", val)
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
