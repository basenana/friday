package actor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	coretools "github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

// newPingTool returns a trivial tool used by tests to verify extra
// tool attachment. The mock agent dispatches tool calls by name from
// the request's toolset.
func newPingTool() *coretools.Tool {
	return &coretools.Tool{
		Name:        "ping_tool",
		Description: "test helper tool",
		InputSchema: coretools.ToolInputSchema{Properties: map[string]interface{}{}},
		Handler: func(ctx context.Context, _ *coretools.Request) (*coretools.Result, error) {
			return coretools.NewToolResultText("pong"), nil
		},
	}
}

// recordingLifecycle captures every lifecycle call in order. It is
// the test fixture for verifying turn boundary behaviour.
type recordingLifecycle struct {
	mu          sync.Mutex
	calls       []string
	evtTypes    []events.EventKind
	evtRunID    []string
	startInfos  []TurnStartInfo
	outcomes    []TurnOutcome
	startErr    error
	finalErr    error
	completeErr error
}

func (r *recordingLifecycle) OnTurnStart(_ context.Context, info TurnStartInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "start:"+info.RunID)
	r.startInfos = append(r.startInfos, info)
	return r.startErr
}
func (r *recordingLifecycle) OnTurnEvent(_ context.Context, runID string, evt events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evtTypes = append(r.evtTypes, evt.Type)
	r.evtRunID = append(r.evtRunID, runID)
}
func (r *recordingLifecycle) OnTurnFinalize(_ context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "finalize")
	return r.finalErr
}
func (r *recordingLifecycle) OnTurnComplete(_ context.Context, runID string, outcome TurnOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "complete:"+runID)
	r.outcomes = append(r.outcomes, outcome)
	return r.completeErr
}

func (r *recordingLifecycle) snapshot() ([]string, []events.EventKind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	evts := make([]events.EventKind, len(r.evtTypes))
	copy(evts, r.evtTypes)
	return out, evts
}

func (r *recordingLifecycle) startInfoSnapshot() []TurnStartInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TurnStartInfo, len(r.startInfos))
	copy(out, r.startInfos)
	return out
}

func (r *recordingLifecycle) outcomeSnapshot() []TurnOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TurnOutcome, len(r.outcomes))
	copy(out, r.outcomes)
	return out
}

type failAgent struct {
	err error
}

func (f failAgent) Chat(context.Context, *api.Request) *api.Response {
	resp := api.NewResponse()
	go func() {
		defer resp.Close()
		resp.Fail(f.err)
	}()
	return resp
}

type blockingEventLifecycle struct {
	released chan struct{}
	once     sync.Once
}

func (b *blockingEventLifecycle) OnTurnStart(context.Context, TurnStartInfo) error { return nil }
func (b *blockingEventLifecycle) OnTurnEvent(ctx context.Context, _ string, evt events.Event) {
	if evt.Type != events.KindRunStarted {
		return
	}
	<-ctx.Done()
	b.once.Do(func() { close(b.released) })
}
func (b *blockingEventLifecycle) OnTurnFinalize(context.Context, string) error { return nil }
func (b *blockingEventLifecycle) OnTurnComplete(context.Context, string, TurnOutcome) error {
	b.once.Do(func() { close(b.released) })
	return nil
}

// TestActor_TurnLifecycleHooks verifies that OnTurnStart →
// OnTurnEvent* → OnTurnFinalize → OnTurnComplete fire in order for a
// successful turn.
func TestActor_TurnLifecycleHooks(t *testing.T) {
	mock := newMockAgent(
		chatScript{deltas: []types.Delta{{Content: "hi"}}},
	)
	lc := &recordingLifecycle{}
	a, _ := newTestActor(mock, WithTurnLifecycle(lc))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = collectEvents(t, sub, hasRunFinished)

	// Give OnTurnComplete a chance to run after RUN_FINISHED is published.
	deadline := time.After(2 * time.Second)
	for {
		calls, _ := lc.snapshot()
		if len(calls) >= 2 && calls[len(calls)-1] != "" && calls[0] != "" && len(calls) >= 3 {
			break
		}
		select {
		case <-deadline:
			calls, _ := lc.snapshot()
			t.Fatalf("lifecycle did not complete in time, calls=%v", calls)
		case <-time.After(10 * time.Millisecond):
		}
	}

	calls, evts := lc.snapshot()
	if len(calls) < 3 {
		t.Fatalf("expected at least start/finalize/complete calls, got %v", calls)
	}
	if calls[0] == "" || calls[0][:6] != "start:" {
		t.Fatalf("first call should be OnTurnStart, got %q", calls[0])
	}
	if calls[len(calls)-1] == "" || calls[len(calls)-1][:9] != "complete:" {
		t.Fatalf("last call should be OnTurnComplete, got %q", calls[len(calls)-1])
	}
	if len(evts) == 0 {
		t.Fatalf("expected OnTurnEvent calls")
	}
	if evts[0] != events.KindRunStarted {
		t.Fatalf("first event should be RUN_STARTED, got %v", evts[0])
	}
}

// TestActor_ExternalTurnID verifies that an inbound UserTextMessage
// with TurnID set propagates verbatim to lifecycle callbacks and
// emitted events.
func TestActor_ExternalTurnID(t *testing.T) {
	mock := newMockAgent(
		chatScript{deltas: []types.Delta{{Content: "ok"}}},
	)
	lc := &recordingLifecycle{}
	a, _ := newTestActor(mock, WithTurnLifecycle(lc))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{
		Text:   "ping",
		TurnID: "ext-turn-abc",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	seen := collectEvents(t, sub, hasRunFinished)
	for _, e := range seen {
		if e.RunID != "ext-turn-abc" {
			t.Fatalf("event runID = %q, want ext-turn-abc (type=%v)", e.RunID, e.Type)
		}
	}
}

// TestActor_TurnIDGenerator verifies that a configured generator is
// consulted when no explicit TurnID is supplied.
func TestActor_TurnIDGenerator(t *testing.T) {
	mock := newMockAgent(
		chatScript{deltas: []types.Delta{{Content: "ok"}}},
	)
	a, _ := newTestActor(mock, WithTurnIDGenerator(func() string { return "gen-xyz" }))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	seen := collectEvents(t, sub, hasRunFinished)
	for _, e := range seen {
		if e.RunID != "gen-xyz" {
			t.Fatalf("event runID = %q, want gen-xyz", e.RunID)
		}
	}
}

// TestActor_ExtraToolsAreAttached verifies that tools injected via
// WithExtraTools reach the agent's Chat call alongside the card tools.
func TestActor_ExtraToolsAreAttached(t *testing.T) {
	// Use a script that invokes our extra tool by name.
	mock := newMockAgent(
		chatScript{
			toolCall: &scriptedToolCall{
				name:      "ping_tool",
				arguments: map[string]any{},
			},
			deltas: []types.Delta{{Content: "pong"}},
		},
	)

	// Build a minimal tool the mock can dispatch.
	ping := newPingTool()
	a, _ := newTestActor(mock, WithExtraTools(ping))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "ping"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	seen := collectEvents(t, sub, hasRunFinished)
	var sawToolStart bool
	for _, e := range seen {
		if e.Type == events.KindToolCallStart {
			var data events.ToolCallStartData
			_ = events.DecodePayload(e, &data)
			if data.ToolName == "ping_tool" {
				sawToolStart = true
			}
		}
	}
	if !sawToolStart {
		t.Fatalf("expected TOOL_CALL_START for ping_tool, events=%v", seen)
	}
}

// TestActor_TurnTimeoutAborts verifies that a turn timeout caps turn
// duration by cancelling the agent context.
func TestActor_TurnTimeoutAborts(t *testing.T) {
	block := make(chan struct{})
	mock := newMockAgent(
		chatScript{
			deltas:     []types.Delta{{Content: "late"}},
			blockUntil: block,
		},
	)
	a, _ := newTestActor(mock, WithTurnTimeout(50*time.Millisecond))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "block"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// The actor should terminate the turn via timeout. RUN_FINISHED
	// must arrive within a few hundred ms.
	seen := collectEvents(t, sub, hasRunFinished)
	if len(seen) == 0 {
		t.Fatalf("expected events including RUN_FINISHED")
	}
	close(block) // release the blocked mock goroutine
}

// TestActor_OnTurnStartError verifies that a lifecycle start failure
// aborts the turn and produces a terminal RunError event.
func TestActor_OnTurnStartError(t *testing.T) {
	mock := newMockAgent(
		chatScript{deltas: []types.Delta{{Content: "should-not-stream"}}},
	)
	lc := &recordingLifecycle{startErr: errors.New("db down")}
	a, _ := newTestActor(mock, WithTurnLifecycle(lc))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var sawRunError bool
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed before RUN_ERROR")
			}
			if e.Type == events.KindRunError {
				sawRunError = true
			}
			if e.Type == events.KindRunFinished {
				// Some flows emit both; tolerate either terminal.
			}
		case <-deadline:
			if !sawRunError {
				t.Fatalf("expected RUN_ERROR on lifecycle start failure")
			}
			return
		}
		if sawRunError {
			return
		}
	}
}

func TestActor_TurnOutcomeCapturesResponseError(t *testing.T) {
	lc := &recordingLifecycle{}
	sess := session.New("test-session", fakeProvider{})
	a := New(failAgent{err: errors.New("provider failed")}, sess, WithTurnLifecycle(lc))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = collectEvents(t, sub, func(seen []events.Event) bool {
		var sawError, sawFinished bool
		for _, e := range seen {
			if e.Type == events.KindRunError {
				sawError = true
			}
			if e.Type == events.KindRunFinished {
				sawFinished = true
			}
		}
		return sawError && sawFinished
	})

	deadline := time.After(2 * time.Second)
	for {
		outcomes := lc.outcomeSnapshot()
		if len(outcomes) > 0 {
			if outcomes[0].Err == nil || outcomes[0].Err.Error() != "provider failed" {
				t.Fatalf("outcome err = %v, want provider failed", outcomes[0].Err)
			}
			if outcomes[0].Cancelled {
				t.Fatalf("unexpected cancelled outcome")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for OnTurnComplete outcome")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestActor_SplitsConflictingTurnIDsIntoSeparateTurns(t *testing.T) {
	mock := newMockAgent(
		chatScript{deltas: []types.Delta{{Content: "first"}}},
		chatScript{deltas: []types.Delta{{Content: "second"}}},
	)
	lc := &recordingLifecycle{}
	a, _ := newTestActor(mock, WithTurnLifecycle(lc))

	if err := a.Send(context.Background(), UserTextMessage{Text: "one", TurnID: "turn-1"}); err != nil {
		t.Fatalf("Send turn-1: %v", err)
	}
	if err := a.Send(context.Background(), UserTextMessage{Text: "two", TurnID: "turn-2"}); err != nil {
		t.Fatalf("Send turn-2: %v", err)
	}

	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	seen := collectEvents(t, sub, func(eventsSeen []events.Event) bool {
		finished := 0
		for _, e := range eventsSeen {
			if e.Type == events.KindRunFinished {
				finished++
			}
		}
		return finished == 2
	})

	var starts []string
	for _, e := range seen {
		if e.Type == events.KindRunStarted {
			starts = append(starts, e.RunID)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("expected 2 RUN_STARTED events, got %v", starts)
	}
	if starts[0] != "turn-1" || starts[1] != "turn-2" {
		t.Fatalf("RUN_STARTED runIDs = %v, want [turn-1 turn-2]", starts)
	}

	startInfos := lc.startInfoSnapshot()
	if len(startInfos) != 2 {
		t.Fatalf("expected 2 OnTurnStart calls, got %d", len(startInfos))
	}
	if startInfos[0].RunID != "turn-1" || startInfos[1].RunID != "turn-2" {
		t.Fatalf("OnTurnStart runIDs = [%s %s], want [turn-1 turn-2]", startInfos[0].RunID, startInfos[1].RunID)
	}
}

func TestActor_TurnMetadataPropagatesToLifecycle(t *testing.T) {
	mock := newMockAgent(chatScript{deltas: []types.Delta{{Content: "ok"}}})
	lc := &recordingLifecycle{}
	a, _ := newTestActor(mock, WithTurnLifecycle(lc))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	msg := UserTextMessage{
		Text: "hello",
		Metadata: map[string]any{
			"trace_id": "trace-123",
			"attempt":  2,
		},
	}
	if err := a.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = collectEvents(t, sub, hasRunFinished)

	startInfos := lc.startInfoSnapshot()
	if len(startInfos) != 1 {
		t.Fatalf("expected 1 OnTurnStart call, got %d", len(startInfos))
	}
	if len(startInfos[0].Metadata) != 1 {
		t.Fatalf("expected 1 metadata entry, got %d", len(startInfos[0].Metadata))
	}
	if got := startInfos[0].Metadata[0]["trace_id"]; got != "trace-123" {
		t.Fatalf("trace_id = %v, want trace-123", got)
	}
	if got := startInfos[0].Metadata[0]["attempt"]; got != 2 {
		t.Fatalf("attempt = %v, want 2", got)
	}
}

func TestActor_TurnMetadataPropagatesToAgentRequest(t *testing.T) {
	mock := newMockAgent(chatScript{deltas: []types.Delta{{Content: "ok"}}})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	msg := UserTextMessage{
		Text: "hello",
		Metadata: map[string]any{
			"session_turn_id": "turn-123",
			"attempt":         2,
		},
	}
	if err := a.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_ = collectEvents(t, sub, hasRunFinished)

	requests := mock.requestSnapshot()
	if len(requests) != 1 {
		t.Fatalf("expected 1 agent request, got %d", len(requests))
	}
	if got := requests[0].Metadata["session_turn_id"]; got != "turn-123" {
		t.Fatalf("session_turn_id = %q, want turn-123", got)
	}
	if _, ok := requests[0].Metadata["attempt"]; ok {
		t.Fatalf("non-string metadata unexpectedly propagated: %#v", requests[0].Metadata)
	}
}

func TestActor_OnTurnEventUsesTurnContext(t *testing.T) {
	mock := newMockAgent(chatScript{deltas: []types.Delta{{Content: "ok"}}})
	lc := &blockingEventLifecycle{released: make(chan struct{})}
	a, _ := newTestActor(mock, WithTurnLifecycle(lc), WithTurnTimeout(50*time.Millisecond))
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case <-lc.released:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("OnTurnEvent did not unblock on turn context cancellation")
	}

	seen := collectEvents(t, sub, hasRunFinished)
	if len(seen) == 0 {
		t.Fatalf("expected turn events after lifecycle release")
	}
}

func TestActor_ImageOnlyMessageStartsTurn(t *testing.T) {
	mock := newMockAgent(chatScript{deltas: []types.Delta{{Content: "saw image"}}})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{
		Images: []types.ImageContent{{
			Type: types.ImageTypeURL,
			ID:   "image-1",
			URL:  "https://example.com/image.png",
		}},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	seen := collectEvents(t, sub, hasRunFinished)
	var sawRunStarted bool
	for _, e := range seen {
		if e.Type == events.KindRunStarted {
			sawRunStarted = true
			break
		}
	}
	if !sawRunStarted {
		t.Fatalf("expected image-only message to start a turn")
	}
}
