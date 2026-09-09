package bus

import (
	"sync"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/core/actor/events"
)

// Feed is an ordered consumer of a set of topics on one bus: a single
// serial listener registered on every pattern, feeding one event channel.
// Because the listener is serial across all its topics, events are
// received in publish order — deltas on one topic never reorder against
// lifecycle markers on another.
//
// The event channel is never closed (the bus handler may still run during
// asynchronous listener drain); consumers that must unblock use Done,
// which closes on Close.
type Feed struct {
	bus    *eventbus.Bus
	ids    []string
	events chan events.Event
	quit   chan struct{}
	once   sync.Once
}

func newFeed(b *eventbus.Bus) *Feed {
	return &Feed{
		bus:    b,
		events: make(chan events.Event, 256),
		quit:   make(chan struct{}),
	}
}

func (f *Feed) handler(env Envelope) {
	select {
	case f.events <- env.Event:
	case <-f.quit:
	}
}

// NewFeed subscribes to the given topic patterns with the given queue
// config (see eventbus.SerialConfig).
func NewFeed(b *eventbus.Bus, cfg eventbus.SerialConfig, patterns ...string) *Feed {
	f := newFeed(b)
	f.ids = []string{b.SubscribeSerial(patterns, f.handler, cfg)}
	return f
}

// SubscribeAgentFeed subscribes to every outbound topic of session sid.
// Delivery is at-most-once: a slow consumer's queue overflows by dropping
// the oldest event (buffer 256).
func SubscribeAgentFeed(b *eventbus.Bus, sid string) *Feed {
	f := newFeed(b)
	f.ids = SubscribeAgent(b, sid, f.handler, eventbus.SerialConfig{
		Buffer:   256,
		Overflow: eventbus.OverflowDropOldest,
	})
	return f
}

// Events returns the ordered event channel.
func (f *Feed) Events() <-chan events.Event { return f.events }

// Done returns a channel closed by Close; use it to unblock consumers
// waiting on Events after teardown.
func (f *Feed) Done() <-chan struct{} { return f.quit }

// Close unsubscribes the feed. Buffered events are discarded.
func (f *Feed) Close() {
	f.once.Do(func() {
		close(f.quit)
		UnsubscribeAll(f.bus, f.ids...)
	})
}
