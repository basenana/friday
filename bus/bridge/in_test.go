package bridge

import (
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
)

func TestInBridgeReportsActorInboxSaturation(t *testing.T) {
	b := eventbus.NewBus()
	a := coreactor.New(nil, nil, coreactor.WithInboxBuffer(1))
	ib := NewInBridge(b, "s1", a)
	drops := make(chan bus.InboxDropped, 1)
	statusID := b.SubscribeSerial([]string{bus.TopicStatus("s1", bus.StatusInboxDropped)}, func(env bus.Envelope) {
		var drop bus.InboxDropped
		if events.DecodePayload(env.Event, &drop) == nil {
			drops <- drop
		}
	}, eventbus.SerialConfig{Overflow: eventbus.OverflowBlock})

	b.Publish(bus.TopicInbox("s1"), bus.NewUserInput("s1", "test", bus.UserTextInput{Text: "first", TurnID: "turn-1"}))
	b.Publish(bus.TopicInbox("s1"), bus.NewUserInput("s1", "test", bus.UserTextInput{Text: "second", TurnID: "turn-2"}))

	select {
	case drop := <-drops:
		if drop.TurnID != "turn-2" || drop.From != "test" || drop.Reason == "" {
			t.Fatalf("unexpected drop: %+v", drop)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbox saturation status")
	}

	ib.Close()
	b.Unsubscribe(statusID)
	b.Wait()
	a.Stop()
}

func TestInBridgeCloseCancelsBlockedPreempt(t *testing.T) {
	b := eventbus.NewBus()
	a := coreactor.New(nil, nil)
	ib := NewInBridge(b, "s1", a)

	// The actor is not started, so its preempt buffer cannot drain. More than
	// the default capacity forces the bridge dispatcher to block in
	// SendPreempt until Close cancels its bridge-scoped context.
	for i := 0; i < 10; i++ {
		b.Publish(bus.TopicPreempt("s1"), bus.NewPreempt("s1", "test", "cancel"))
	}
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		ib.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("InBridge.Close blocked behind a saturated preempt inbox")
	}
	b.Wait()
	a.Stop()
}
