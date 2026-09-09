package actor

import (
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/core/actor/events"
)

func subscribeStatus(t *testing.T, b *eventbus.Bus, sid string) chan bus.Envelope {
	t.Helper()
	ch := make(chan bus.Envelope, 16)
	id := b.SubscribeSerial([]string{bus.TopicStatus(sid, "*")}, func(env bus.Envelope) { ch <- env },
		eventbus.SerialConfig{Overflow: eventbus.OverflowDropOldest})
	t.Cleanup(func() { b.Unsubscribe(id) })
	return ch
}

func expectStatus(t *testing.T, ch chan bus.Envelope, event string) bus.Envelope {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case env := <-ch:
			if env.Topic == bus.TopicStatus(env.Session, event) {
				return env
			}
		case <-time.After(50 * time.Millisecond):
			// keep waiting until the deadline
		case <-deadline:
			t.Fatalf("timed out waiting for status %q", event)
		}
	}
}

func TestRegistryBus_StatusCreatedStopped(t *testing.T) {
	r := newTestRegistry(t, nil)
	sid := "sess-1"
	ch := subscribeStatus(t, r.Bus(), sid)
	defer r.ShutdownAll()

	a, err := r.GetOrCreate(sid)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	env := expectStatus(t, ch, bus.StatusCreated)
	if env.ActorID != a.ID() {
		t.Fatalf("created envelope ActorID = %q, want actor id %q", env.ActorID, a.ID())
	}

	r.Shutdown(sid)
	expectStatus(t, ch, bus.StatusStopped)
}

func TestRegistryBus_StatusEvicted(t *testing.T) {
	r := newTestRegistry(t, func(c *RegistryConfig) {
		c.IdleTimeout = 50 * time.Millisecond
		c.SweepInterval = 20 * time.Millisecond
	})
	sid := "sess-1"
	ch := subscribeStatus(t, r.Bus(), sid)
	defer r.ShutdownAll()

	if _, err := r.GetOrCreate(sid); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	expectStatus(t, ch, bus.StatusEvicted)
	expectStatus(t, ch, bus.StatusStopped)
}

func TestRegistryBus_ShutdownAllPublishesStopped(t *testing.T) {
	r := newTestRegistry(t, nil)
	chA := subscribeStatus(t, r.Bus(), "sess-a")
	chB := subscribeStatus(t, r.Bus(), "sess-b")

	for _, sid := range []string{"sess-a", "sess-b"} {
		if _, err := r.GetOrCreate(sid); err != nil {
			t.Fatalf("GetOrCreate(%s): %v", sid, err)
		}
	}
	expectStatus(t, chA, bus.StatusCreated)
	expectStatus(t, chB, bus.StatusCreated)

	r.ShutdownAll()
	expectStatus(t, chA, bus.StatusStopped)
	expectStatus(t, chB, bus.StatusStopped)
}

func TestRegistryBus_InboxDroppedOnUnknownKind(t *testing.T) {
	r := newTestRegistry(t, nil)
	sid := "sess-1"
	ch := subscribeStatus(t, r.Bus(), sid)
	defer r.ShutdownAll()

	if _, err := r.GetOrCreate(sid); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	expectStatus(t, ch, bus.StatusCreated)

	evt := events.NewEvent(events.KindCustom, "").WithName("bogus.kind")
	r.Bus().Publish(bus.TopicInbox(sid), bus.Envelope{
		Event:   evt,
		Topic:   bus.TopicInbox(sid),
		Session: sid,
		From:    "test",
		TS:      bus.NextTS(),
	})

	deadline := time.After(3 * time.Second)
	for {
		select {
		case env := <-ch:
			if env.Topic != bus.TopicStatus(sid, bus.StatusInboxDropped) {
				continue
			}
			var drop bus.InboxDropped
			if err := events.DecodePayload(env.Event, &drop); err != nil {
				t.Fatalf("decode inbox_dropped payload: %v", err)
			}
			if drop.From != "test" || drop.Reason == "" {
				t.Fatalf("inbox_dropped = %+v, want From=test and a reason", drop)
			}
			return
		case <-time.After(50 * time.Millisecond):
			// keep waiting until the deadline
		case <-deadline:
			t.Fatalf("timed out waiting for status.inbox_dropped")
		}
	}
}
