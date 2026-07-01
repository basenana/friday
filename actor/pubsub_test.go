package actor

import (
	"testing"
	"time"
)

func TestSessionPubSubDeliversEventsInOrder(t *testing.T) {
	pubsub := newSessionPubSub()
	events := make(chan Event)

	done := make(chan struct{})
	go func() {
		defer close(done)
		pubsub.run(events, nil)
	}()

	sub, unsubscribe := pubsub.subscribe(4)
	defer unsubscribe()

	events <- Event{Type: EventTextMessageContent, Seq: 1, Data: map[string]any{"delta": "one"}}
	events <- Event{Type: EventRunFinished, Seq: 2, Data: map[string]any{"stop_reason": "end_turn"}}
	close(events)

	var got []EventType
	for evt := range sub {
		got = append(got, evt.Type)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pubsub did not stop after event channel close")
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0] != EventTextMessageContent || got[1] != EventRunFinished {
		t.Fatalf("unexpected event order: %v", got)
	}
}

func TestSessionPubSubUnsubscribeClosesSubscriber(t *testing.T) {
	pubsub := newSessionPubSub()
	events := make(chan Event)

	go pubsub.run(events, nil)

	sub, unsubscribe := pubsub.subscribe(1)
	unsubscribe()

	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("expected subscriber channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("unsubscribe did not close subscriber channel")
	}

	close(events)
	pubsub.stop()
}
