package actor

import (
	"log"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
)

// TestEventStream_TerminalEviction verifies that when a subscriber
// buffer fills up, terminal events still get through by evicting
// older non-terminal events.
func TestEventStream_TerminalEviction(t *testing.T) {
	s := NewEventStream(log.New(&bytesBuffer{}, "", 0))
	// Buffer of 2 so we can fill it deterministically.
	sub := s.Subscribe(2)

	// Fill the buffer.
	s.Publish(events.NewEvent(events.KindTextMessageContent, "r1"))
	s.Publish(events.NewEvent(events.KindTextMessageContent, "r1"))

	// One more non-terminal — should be dropped.
	s.Publish(events.NewEvent(events.KindTextMessageContent, "r1"))

	// A terminal event must arrive even though the buffer was full.
	s.Publish(events.NewEvent(events.KindRunFinished, "r1").WithPayload(events.RunFinishedData{}))

	// Drain and assert.
	deadline := time.After(time.Second)
	var got []events.Event
collect:
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				break collect
			}
			got = append(got, e)
			if len(got) >= 2 {
				break collect
			}
		case <-deadline:
			t.Fatalf("timeout draining events, got %d", len(got))
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events after eviction, got %d", len(got))
	}
	if got[1].Type != events.KindRunFinished {
		t.Fatalf("expected RUN_FINISHED to survive eviction, got %v first", got[1].Type)
	}
}

func TestEventStream_PreservesDistinctTerminalEvents(t *testing.T) {
	s := NewEventStream(log.New(&bytesBuffer{}, "", 0))
	sub := s.Subscribe(2)

	s.Publish(events.NewEvent(events.KindTextMessageContent, "r1"))
	s.Publish(events.NewEvent(events.KindRunError, "r1").WithPayload(events.RunErrorData{Message: "boom"}))
	s.Publish(events.NewEvent(events.KindRunFinished, "r1").WithPayload(events.RunFinishedData{}))

	deadline := time.After(time.Second)
	var got []events.Event
	for len(got) < 2 {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed after %d events", len(got))
			}
			got = append(got, e)
		case <-deadline:
			t.Fatalf("timeout draining terminal events, got %d", len(got))
		}
	}

	if got[0].Type != events.KindRunError || got[1].Type != events.KindRunFinished {
		t.Fatalf("terminal order = [%s %s], want [RUN_ERROR RUN_FINISHED]", got[0].Type, got[1].Type)
	}
}

// bytesBuffer is a minimal io.Writer backing the test logger.
type bytesBuffer struct {
	data []byte
}

func (b *bytesBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}
