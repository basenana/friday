package actor

import (
	"context"
	"errors"
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
	if got := drainStopReason(t, sub); got != "cancelled" {
		t.Fatalf("stop reason = %q, want cancelled", got)
	}
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
