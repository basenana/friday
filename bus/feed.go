package bus

import (
	"sync"
	"sync/atomic"

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
	bus     *eventbus.Bus
	ids     []string
	events  chan events.Event
	quit    chan struct{}
	once    sync.Once
	dropped atomic.Uint64
}

const defaultFeedBuffer = 512

func newFeed(b *eventbus.Bus) *Feed {
	return &Feed{
		bus:    b,
		events: make(chan events.Event, defaultFeedBuffer),
		quit:   make(chan struct{}),
	}
}

func (f *Feed) handler(env Envelope) {
	evt := env.Event
	if evt.ActorID == "" {
		evt.ActorID = env.ActorID
	}
	if evt.Seq == 0 && env.Seq > 0 {
		evt.Seq = int64(env.Seq)
	}
	select {
	case f.events <- evt:
	case <-f.quit:
	}
}

// NewFeed subscribes to the given topic patterns with the given queue
// config (see eventbus.SerialConfig).
func NewFeed(b *eventbus.Bus, cfg eventbus.SerialConfig, patterns ...string) *Feed {
	f := newFeed(b)
	cfg = f.withDropAccounting(cfg)
	f.ids = []string{b.SubscribeSerial(patterns, f.handler, cfg)}
	return f
}

// SubscribeAgentFeed subscribes to every outbound topic of session sid.
// Delivery is at-most-once: a slow consumer's queue overflows by dropping
// the oldest event (buffer 512).
func SubscribeAgentFeed(b *eventbus.Bus, sid string) *Feed {
	f := newFeed(b)
	cfg := f.withDropAccounting(eventbus.SerialConfig{
		Buffer:   defaultFeedBuffer,
		Overflow: eventbus.OverflowDropOldest,
	})
	f.ids = SubscribeAgent(b, sid, f.handler, cfg)
	return f
}

func (f *Feed) withDropAccounting(cfg eventbus.SerialConfig) eventbus.SerialConfig {
	if cfg.Overflow != eventbus.OverflowDropOldest {
		return cfg
	}
	previous := cfg.OnDrop
	cfg.OnDrop = func(count uint64) {
		f.dropped.Add(count)
		if previous != nil {
			previous(count)
		}
	}
	return cfg
}

// Events returns the ordered event channel.
func (f *Feed) Events() <-chan events.Event { return f.events }

// Dropped returns the number of events evicted from this feed's serial queue.
func (f *Feed) Dropped() uint64 { return f.dropped.Load() }

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
