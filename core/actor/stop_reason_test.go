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

func TestSteerInterruptsAndRunsBeforeQueuedInput(t *testing.T) {
	blocked := make(chan struct{})
	mock := newMockAgent(
		chatScript{blockUntil: blocked},
		chatScript{deltas: []types.Delta{{Content: "steered"}}},
		chatScript{deltas: []types.Delta{{Content: "queued"}}},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.Send(context.Background(), UserTextMessage{Text: "original", TurnID: "original"}); err != nil {
		t.Fatal(err)
	}
	for {
		e := <-sub.Events()
		if e.Type == events.KindRunStarted {
			break
		}
	}
	if err := a.Send(context.Background(), UserTextMessage{Text: "later", TurnID: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SendSteer(context.Background(), UserTextMessage{Text: "redirect", TurnID: "steer"}); err != nil {
		t.Fatal(err)
	}

	var starts []string
	deadline := time.After(3 * time.Second)
	for len(starts) < 2 {
		select {
		case e := <-sub.Events():
			if e.Type == events.KindRunStarted && e.RunID != "original" {
				starts = append(starts, e.RunID)
			}
		case <-deadline:
			t.Fatalf("timed out; starts=%v", starts)
		}
	}
	if want := []string{"steer", "queued"}; !reflect.DeepEqual(starts, want) {
		t.Fatalf("run order = %v, want %v", starts, want)
	}
}

func TestCurrentPreemptPreservesQueuedInput(t *testing.T) {
	blocked := make(chan struct{})
	mock := newMockAgent(chatScript{blockUntil: blocked}, chatScript{deltas: []types.Delta{{Content: "next"}}})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	_ = a.Send(context.Background(), UserTextMessage{Text: "original", TurnID: "original"})
	for {
		if e := <-sub.Events(); e.Type == events.KindRunStarted {
			break
		}
	}
	_ = a.Send(context.Background(), UserTextMessage{Text: "keep", TurnID: "keep"})
	if err := a.SendPreemptScope(context.Background(), "cancel current", PreemptCurrent); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-sub.Events():
			if e.Type == events.KindRunStarted && e.RunID == "keep" {
				return
			}
		case <-deadline:
			t.Fatal("queued input was not run after current-only preempt")
		}
	}
}

func TestIdleSteerStartsWithoutNormalInboxWakeup(t *testing.T) {
	mock := newMockAgent(chatScript{deltas: []types.Delta{{Content: "steered"}}})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	if err := a.SendSteer(context.Background(), UserTextMessage{Text: "redirect", TurnID: "steer-idle"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-sub.Events():
			if e.Type == events.KindRunFinished && e.RunID == "steer-idle" {
				var data events.RunFinishedData
				if err := events.DecodePayload(e, &data); err != nil {
					t.Fatal(err)
				}
				if data.StopReason != "end_turn" {
					t.Fatalf("idle steer stop reason = %q", data.StopReason)
				}
				return
			}
		case <-deadline:
			t.Fatal("idle steer did not wake the actor")
		}
	}
}

func TestLatestSteerSupersedesOrCancelsEarlierSteer(t *testing.T) {
	original := make(chan struct{})
	firstSteer := make(chan struct{})
	mock := newMockAgent(
		chatScript{blockUntil: original},
		chatScript{blockUntil: firstSteer},
		chatScript{deltas: []types.Delta{{Content: "latest"}}},
	)
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	_ = a.Send(context.Background(), UserTextMessage{Text: "original", TurnID: "original"})
	for {
		if e := <-sub.Events(); e.Type == events.KindRunStarted {
			break
		}
	}
	if err := a.SendSteer(context.Background(), UserTextMessage{Text: "first", TurnID: "steer-1"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SendSteer(context.Background(), UserTextMessage{Text: "second", TurnID: "steer-2"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-sub.Events():
			if e.Type == events.KindRunStarted && e.RunID == "steer-2" {
				return
			}
		case <-deadline:
			t.Fatal("latest steer never became active")
		}
	}
}

func TestPreemptAllDiscardsPendingSteer(t *testing.T) {
	blocked := make(chan struct{})
	mock := newMockAgent(chatScript{blockUntil: blocked}, chatScript{deltas: []types.Delta{{Content: "unexpected"}}})
	a, _ := newTestActor(mock)
	sub := a.Subscribe()
	a.Start(context.Background())
	defer a.Stop()

	_ = a.Send(context.Background(), UserTextMessage{Text: "original", TurnID: "original"})
	for {
		if e := <-sub.Events(); e.Type == events.KindRunStarted {
			break
		}
	}
	if err := a.SendSteer(context.Background(), UserTextMessage{Text: "discard", TurnID: "steer"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SendPreempt(context.Background(), "cancel all"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case e := <-sub.Events():
			if e.Type == events.KindRunStarted && e.RunID == "steer" {
				t.Fatal("cancel-all allowed a pending steer to run")
			}
		case <-deadline:
			return
		}
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
