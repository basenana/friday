package actor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
)

// drainStopReason consumes events until RUN_FINISHED and returns its
// StopReason payload.
func drainStopReason(t *testing.T, sub *Subscription) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed before RUN_FINISHED")
			}
			if e.Type != events.KindRunFinished {
				continue
			}
			var d events.RunFinishedData
			if err := events.DecodePayload(e, &d); err != nil {
				t.Fatalf("decode RunFinishedData: %v", err)
			}
			return d.StopReason
		case <-deadline:
			t.Fatalf("timeout waiting for RUN_FINISHED")
		}
	}
}

func TestRunFinishedStopReason_EndTurn(t *testing.T) {
	mock := newMockAgent(chatScript{deltas: []types.Delta{{Content: "hi"}}})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := drainStopReason(t, sub); got != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", got)
	}
}

func TestRunFinishedIncludesActorDuration(t *testing.T) {
	mock := newMockAgent(chatScript{deltas: []types.Delta{{Content: "hi"}}, delay: 20 * time.Millisecond})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatal(err)
	}
	for {
		evt := <-sub.Events()
		if evt.Type != events.KindRunFinished {
			continue
		}
		var data events.RunFinishedData
		if err := events.DecodePayload(evt, &data); err != nil {
			t.Fatal(err)
		}
		if data.DurationMs < 15 {
			t.Fatalf("duration_ms = %d", data.DurationMs)
		}
		return
	}
}

func TestRunFinishedStopReason_Error(t *testing.T) {
	mock := newMockAgent(chatScript{streamErr: errors.New("llm exploded")})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "go"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := drainStopReason(t, sub); got != "error" {
		t.Fatalf("stop reason = %q, want error", got)
	}
}

func TestRunFinishedStopReason_Cancelled(t *testing.T) {
	blocked := make(chan struct{})
	mock := newMockAgent(chatScript{blockUntil: blocked})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "long task"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Wait until the turn is running, then preempt it.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed before RUN_STARTED")
			}
			if e.Type == events.KindRunStarted {
				goto preempt
			}
		case <-deadline:
			t.Fatalf("timeout waiting for RUN_STARTED")
		}
	}
preempt:
	if err := a.SendPreempt(context.Background(), "user cancelled"); err != nil {
		t.Fatalf("SendPreempt: %v", err)
	}
	seen := collectEvents(t, sub, hasRunFinished)
	var stopReason string
	for _, evt := range seen {
		if evt.Type == events.KindRunError {
			t.Fatalf("preempt emitted RUN_ERROR: %+v", evt)
		}
		if evt.Type == events.KindRunFinished {
			var data events.RunFinishedData
			if err := events.DecodePayload(evt, &data); err != nil {
				t.Fatal(err)
			}
			stopReason = data.StopReason
		}
	}
	if stopReason != "cancelled" {
		t.Fatalf("stop reason = %q, want cancelled", stopReason)
	}
}

func TestNormalInputsWaitForCurrentTurnAndRunFIFO(t *testing.T) {
	release := make(chan struct{})
	mock := newMockAgent(
		chatScript{blockUntil: release},
		chatScript{deltas: []types.Delta{{Content: "second"}}},
		chatScript{deltas: []types.Delta{{Content: "third"}}},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	sendUserText(t, a, "first", "turn-a")
	waitForRunStart(t, sub, "turn-a")
	sendUserText(t, a, "second", "turn-b")
	sendUserText(t, a, "third", "turn-c")

	deadline := time.After(100 * time.Millisecond)
waitForNoStart:
	for {
		select {
		case e := <-sub.Events():
			if e.Type == events.KindRunStarted {
				t.Fatalf("normal input interrupted active run: started %q", e.RunID)
			}
		case <-deadline:
			break waitForNoStart
		}
	}
	if got := len(mock.requestSnapshot()); got != 1 {
		t.Fatalf("agent calls before releasing current turn = %d, want 1", got)
	}

	close(release)
	starts := collectRunStarts(t, sub, 2)
	if want := []string{"turn-b", "turn-c"}; !reflect.DeepEqual(starts, want) {
		t.Fatalf("run order = %v, want %v", starts, want)
	}
}

func TestCurrentPreemptPreservesQueuedInputsFIFO(t *testing.T) {
	blocked := make(chan struct{})
	mock := newMockAgent(
		chatScript{blockUntil: blocked},
		chatScript{deltas: []types.Delta{{Content: "second"}}},
		chatScript{deltas: []types.Delta{{Content: "third"}}},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	sendUserText(t, a, "first", "turn-a")
	waitForRunStart(t, sub, "turn-a")
	sendUserText(t, a, "second", "turn-b")
	sendUserText(t, a, "third", "turn-c")
	if err := a.SendPreemptScope(context.Background(), "cancel current", PreemptCurrent); err != nil {
		t.Fatal(err)
	}

	if got := waitForRunFinish(t, sub, "turn-a"); got != "cancelled" {
		t.Fatalf("turn-a stop reason = %q, want cancelled", got)
	}
	starts := collectRunStarts(t, sub, 2)
	if want := []string{"turn-b", "turn-c"}; !reflect.DeepEqual(starts, want) {
		t.Fatalf("run order = %v, want %v", starts, want)
	}
}

func TestCurrentPreemptWithoutQueuedInputBecomesIdle(t *testing.T) {
	blocked := make(chan struct{})
	mock := newMockAgent(chatScript{blockUntil: blocked})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	sendUserText(t, a, "first", "turn-a")
	waitForRunStart(t, sub, "turn-a")
	if err := a.SendPreemptScope(context.Background(), "cancel current", PreemptCurrent); err != nil {
		t.Fatal(err)
	}
	if got := waitForRunFinish(t, sub, "turn-a"); got != "cancelled" {
		t.Fatalf("turn-a stop reason = %q, want cancelled", got)
	}

	select {
	case e := <-sub.Events():
		if e.Type == events.KindRunStarted {
			t.Fatalf("unexpected follow-up run %q", e.RunID)
		}
	case <-time.After(100 * time.Millisecond):
	}
	if got := len(mock.requestSnapshot()); got != 1 {
		t.Fatalf("agent calls = %d, want 1", got)
	}
}

func TestPreemptAllDiscardsPendingNormalInput(t *testing.T) {
	blocked := make(chan struct{})
	mock := newMockAgent(
		chatScript{blockUntil: blocked},
		chatScript{deltas: []types.Delta{{Content: "unexpected"}}},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	sendUserText(t, a, "first", "turn-a")
	waitForRunStart(t, sub, "turn-a")
	sendUserText(t, a, "discard", "turn-b")
	if err := a.SendPreempt(context.Background(), "cancel all"); err != nil {
		t.Fatal(err)
	}
	if got := waitForRunFinish(t, sub, "turn-a"); got != "cancelled" {
		t.Fatalf("turn-a stop reason = %q, want cancelled", got)
	}

	select {
	case e := <-sub.Events():
		if e.Type == events.KindRunStarted {
			t.Fatalf("cancel-all allowed pending run %q", e.RunID)
		}
	case <-time.After(100 * time.Millisecond):
	}
	if got := len(mock.requestSnapshot()); got != 1 {
		t.Fatalf("agent calls = %d, want 1", got)
	}
}

func sendUserText(t *testing.T, a *Actor, text, turnID string) {
	t.Helper()
	if err := a.Send(context.Background(), UserTextMessage{Text: text, TurnID: turnID}); err != nil {
		t.Fatalf("send %s: %v", turnID, err)
	}
}

func waitForRunStart(t *testing.T, sub *Subscription, runID string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed before %s started", runID)
			}
			if e.Type == events.KindRunStarted && e.RunID == runID {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s to start", runID)
		}
	}
}

func waitForRunFinish(t *testing.T, sub *Subscription, runID string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed before %s finished", runID)
			}
			if e.Type != events.KindRunFinished || e.RunID != runID {
				continue
			}
			var data events.RunFinishedData
			if err := events.DecodePayload(e, &data); err != nil {
				t.Fatal(err)
			}
			return data.StopReason
		case <-deadline:
			t.Fatalf("timeout waiting for %s to finish", runID)
		}
	}
}

func collectRunStarts(t *testing.T, sub *Subscription, count int) []string {
	t.Helper()
	starts := make([]string, 0, count)
	deadline := time.After(3 * time.Second)
	for len(starts) < count {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed after starts %v", starts)
			}
			if e.Type == events.KindRunStarted {
				starts = append(starts, e.RunID)
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %d run starts; got %v", count, starts)
		}
	}
	return starts
}

func TestStepFinishedCarriesCoreEventData(t *testing.T) {
	// The translator must forward core model.finish event data (e.g.
	// total_tokens) on STEP_FINISHED payloads.
	tr := NewTranslator("run-1")
	out := tr.FromCoreEvent(types.Event{
		Type: types.EventModelFinish,
		Data: map[string]string{"total_tokens": "1234"},
	})
	found := false
	for _, e := range out {
		if e.Type != events.KindStepFinished {
			continue
		}
		var d events.StepFinishedData
		if err := events.DecodePayload(e, &d); err != nil {
			t.Fatalf("decode StepFinishedData: %v", err)
		}
		if d.Kind != "model" {
			t.Fatalf("kind = %q, want model", d.Kind)
		}
		if d.Data["total_tokens"] != "1234" {
			t.Fatalf("data[total_tokens] = %q, want 1234", d.Data["total_tokens"])
		}
		found = true
	}
	if !found {
		t.Fatalf("expected a STEP_FINISHED event")
	}
}
