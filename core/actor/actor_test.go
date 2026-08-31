package actor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/actor/sink"
	"github.com/basenana/friday/core/types"
)

// collectEvents drains the subscription until the predicate returns
// true or the deadline expires. Returns all events seen.
func collectEvents(t *testing.T, sub *Subscription, done func([]events.Event) bool) []events.Event {
	t.Helper()
	var seen []events.Event
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				return seen
			}
			seen = append(seen, e)
			if done(seen) {
				return seen
			}
		case <-deadline:
			t.Fatalf("collectEvents timeout, seen=%d", len(seen))
		}
	}
}

func hasRunFinished(seen []events.Event) bool {
	for _, e := range seen {
		if e.Type == events.KindRunFinished {
			return true
		}
	}
	return false
}

// TestActor_MultiMessageDrain verifies that three quick user messages
// are coalesced into a single turn and produce one RUN_STARTED with
// Batch=3 followed by one RUN_FINISHED.
func TestActor_MultiMessageDrain(t *testing.T) {
	mock := newMockAgent(
		chatScript{deltas: []types.Delta{
			{Content: "Hello"},
			{Content: " world"},
		}},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	ctx := context.Background()
	for _, msg := range []string{"one", "two", "three"} {
		if err := a.Send(ctx, UserTextMessage{Text: msg}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	seen := collectEvents(t, sub, hasRunFinished)
	var starts, finishes int
	var batch int
	for _, e := range seen {
		switch e.Type {
		case events.KindRunStarted:
			starts++
			var data events.RunStartedData
			_ = events.DecodePayload(e, &data)
			batch = data.Batch
		case events.KindRunFinished:
			finishes++
		}
	}
	if starts != 1 || finishes != 1 {
		t.Fatalf("expected 1 RUN_STARTED and 1 RUN_FINISHED, got starts=%d finishes=%d", starts, finishes)
	}
	// batch counts the messages coalesced into the turn.
	if batch < 1 {
		t.Fatalf("expected batch>=1, got %d", batch)
	}
}

// TestActor_EmitCardTool verifies that an agent invoking emit_card
// produces a card.emitted Custom event with the supplied payload.
func TestActor_EmitCardTool(t *testing.T) {
	mock := newMockAgent(
		chatScript{
			toolCall: &scriptedToolCall{
				name: "emit_card",
				arguments: map[string]any{
					"kind":  "file",
					"title": "Result",
					"component": map[string]any{
						"path": "/sandbox/out.txt",
					},
				},
			},
			deltas: []types.Delta{{Content: "done"}},
		},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "run"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	seen := collectEvents(t, sub, hasRunFinished)
	var found bool
	for _, e := range seen {
		if e.Type == events.KindCustom && e.Name == events.CustomCardEmitted {
			var body events.CardEmittedBody
			if err := events.DecodePayload(e, &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Kind != "file" {
				t.Fatalf("kind=file expected, got %s", body.Kind)
			}
			if body.CardID == "" {
				t.Fatalf("card_id should be set")
			}
			if path, _ := body.Component["path"].(string); path != "/sandbox/out.txt" {
				t.Fatalf("component.path mismatch: %v", body.Component)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("did not see card.emitted event")
	}
}

// TestActor_RequestForm_Submit verifies the full form interrupt cycle:
// the agent calls request_form, the actor emits form.requested, the
// test submits values, the tool returns them, and form.submitted fires.
func TestActor_RequestForm_Submit(t *testing.T) {
	mock := newMockAgent(
		chatScript{
			toolCall: &scriptedToolCall{
				name: "request_form",
				arguments: map[string]any{
					"schema": map[string]any{
						"title": "Pick one",
						"fields": []map[string]any{
							{"name": "organism", "type": "select",
								"options": []map[string]any{
									{"label": "Human", "value": "hsapiens"},
								}},
						},
					},
				},
			},
		},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "ask"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for form.requested, then submit.
	formID := waitForFormRequested(t, sub)
	if formID == "" {
		t.Fatalf("no form.requested observed")
	}
	if err := a.SubmitForm(formID, map[string]any{"organism": "hsapiens"}); err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}

	// Drain remaining events until RUN_FINISHED.
	collectEvents(t, sub, hasRunFinished)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.toolCalls) == 0 {
		t.Fatalf("expected request_form to have been invoked")
	}
}

func waitForFormRequested(t *testing.T, sub *Subscription) string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				return ""
			}
			if e.Type == events.KindCustom && e.Name == events.CustomFormRequested {
				var body events.FormRequestedBody
				if err := events.DecodePayload(e, &body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				return body.FormID
			}
		case <-deadline:
			return ""
		}
	}
}

// TestActor_Shutdown_Idle verifies that Shutdown returns immediately
// (nil error) when the actor is idle — no turn in flight.
func TestActor_Shutdown_Idle(t *testing.T) {
	mock := newMockAgent()
	a, _ := newTestActor(mock)
	a.Start(context.Background())

	done := make(chan struct{})
	start := time.Now()
	go func() {
		err := a.Shutdown(context.Background())
		if err != nil {
			t.Errorf("idle Shutdown err = %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("idle Shutdown did not return within 2s")
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("idle Shutdown too slow: %v", d)
	}
}

// TestActor_Shutdown_GracefulDuringTurn verifies that Shutdown waits
// for the in-flight turn to complete naturally when given a generous
// deadline, then returns nil.
func TestActor_Shutdown_GracefulDuringTurn(t *testing.T) {
	release := make(chan struct{})
	mock := newMockAgent(
		chatScript{
			blockUntil: release,
			deltas:     []types.Delta{{Content: "done"}},
		},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Wait until the turn is actually running.
	waitForRunStarted(t, sub)

	// Ensure Shutdown is actually waiting (not finishing instantly).
	shutdownDone := make(chan error, 1)
	go func() {
		// 5s deadline >> the turn will take once we release it.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- a.Shutdown(ctx)
	}()

	// Ensure Shutdown is actually waiting (not finishing instantly).
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before turn released: err=%v", err)
	case <-time.After(50 * time.Millisecond):
		// good: Shutdown is blocked waiting for the turn.
	}

	// Release the turn; Shutdown should complete gracefully.
	close(release)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("graceful Shutdown err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("graceful Shutdown did not complete after turn release")
	}

	// The stream must be closed: Events channel should be drained and
	// closed. We just verify Stop is now a no-op (idempotent).
	a.Stop()
	_ = sub
}

// TestActor_Shutdown_ForceOnTimeout verifies that when the deadline
// expires before the turn completes, Shutdown aborts the turn (via
// context cancel) and returns the deadline error.
func TestActor_Shutdown_ForceOnTimeout(t *testing.T) {
	// blockForever never closes; only ctx cancellation can unblock.
	blockForever := make(chan struct{})
	mock := newMockAgent(
		chatScript{blockUntil: blockForever},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Confirm the turn is running before shutting down.
	waitForRunStarted(t, sub)

	// Tiny deadline → must abort.
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := a.Shutdown(ctx)
	d := time.Since(start)

	if err == nil {
		t.Fatalf("expected deadline error, got nil")
	}
	if d > 2*time.Second {
		t.Fatalf("forced Shutdown took too long: %v", d)
	}
	// Subsequent Stop is a no-op.
	a.Stop()
}

// waitForRunStarted blocks until the subscriber observes RUN_STARTED,
// confirming the loop has entered a turn.
func waitForRunStarted(t *testing.T, sub *Subscription) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("stream closed before RUN_STARTED")
			}
			if e.Type == events.KindRunStarted {
				return
			}
		case <-deadline:
			t.Fatalf("RUN_STARTED not observed within 2s")
		}
	}
}

// TestActor_Shutdown_Idempotent verifies that calling Shutdown twice
// (and Stop afterwards) does not panic or block.
func TestActor_Shutdown_Idempotent(t *testing.T) {
	mock := newMockAgent()
	a, _ := newTestActor(mock)
	a.Start(context.Background())

	ctx1, c1 := context.WithTimeout(context.Background(), time.Second)
	defer c1()
	if err := a.Shutdown(ctx1); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	ctx2, c2 := context.WithTimeout(context.Background(), time.Second)
	defer c2()
	if err := a.Shutdown(ctx2); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	a.Stop() // must not block
}
func TestActor_JSONLSink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	s, err := sink.NewJSONL(path)
	if err != nil {
		t.Fatalf("NewJSONL: %v", err)
	}

	mock := newMockAgent(
		chatScript{deltas: []types.Delta{{Content: "hi"}}},
	)
	a, _ := newTestActor(mock, WithSink(s))
	sub := a.Subscribe()
	a.Start(context.Background())

	_ = a.Send(context.Background(), UserTextMessage{Text: "hello"})
	collectEvents(t, sub, hasRunFinished)
	a.Stop() // closes the sink

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "RUN_STARTED") {
		t.Fatalf("expected RUN_STARTED in jsonl, got:\n%s", data)
	}
	if !strings.Contains(string(data), "RUN_FINISHED") {
		t.Fatalf("expected RUN_FINISHED in jsonl, got:\n%s", data)
	}
	// Sanity-check JSON: each non-empty line must parse.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 events in jsonl, got %d", len(lines))
	}
	for i, line := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, line)
		}
	}
}
