package actor

import (
	"log"
	"sync"

	"github.com/basenana/friday/core/actor/events"
)

// EventStream fans out events to multiple subscribers. Publish is
// non-blocking: subscribers with full buffers are dropped (with a
// warning log) rather than blocking the producer.
type EventStream struct {
	mu   sync.RWMutex
	subs []*Subscription

	logger *log.Logger
	closed bool
}

// NewEventStream builds a fresh stream. logger may be nil; a default
// discard logger is used in that case.
func NewEventStream(logger *log.Logger) *EventStream {
	if logger == nil {
		logger = log.New(devZero{}, "", 0)
	}
	return &EventStream{logger: logger}
}

// Subscribe registers a new subscriber. The returned Subscription's
// Events channel receives subsequent Publish calls. Close the
// subscription when done to release resources.
func (s *EventStream) Subscribe(buffer int) *Subscription {
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}
	sub := &Subscription{
		events: make(chan events.Event, buffer),
		stream: s,
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		close(sub.events)
		return sub
	}
	s.subs = append(s.subs, sub)
	s.mu.Unlock()
	return sub
}

// Publish fans evt out to every subscriber.
//
// When a subscriber's buffer is full, terminal events are prioritized
// over non-terminal ones: non-terminal backlog is evicted first so
// subscribers still observe how a turn ended. Identical terminal
// events for the same run are collapsed, and when the queue is already
// saturated with terminals the oldest terminal is dropped in favour of
// the newest one. Drop behaviour is logged at the stream's logger.
func (s *EventStream) Publish(evt events.Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sub := range s.subs {
		sub.publish(evt, s.logger)
	}
}

// publish delivers evt to a single subscriber. On a full buffer:
//   - terminal events (RUN_FINISHED, RUN_ERROR) evict queued
//     non-terminal events first, preserving as many terminal events as
//     will fit while keeping the newest terminal;
//   - non-terminal events are dropped (logged).
func (sub *Subscription) publish(evt events.Event, logger *log.Logger) {
	sub.mu.Lock()
	defer sub.mu.Unlock()

	// Fast path: buffer has room.
	select {
	case sub.events <- evt:
		return
	default:
	}

	if !IsTerminal(evt) {
		if logger != nil {
			logger.Printf("actor: event stream subscriber buffer full, dropping event type=%s", evt.Type)
		}
		return
	}

	queued := make([]events.Event, 0, cap(sub.events))
	for {
		select {
		case old, ok := <-sub.events:
			if !ok {
				return
			}
			queued = append(queued, old)
		default:
			goto rebuild
		}
	}

rebuild:
	capacity := cap(sub.events)
	keepTerminal := make([]bool, len(queued))
	duplicate := false
	terminalCount := 0
	for i, old := range queued {
		if !IsTerminal(old) {
			continue
		}
		keepTerminal[i] = true
		terminalCount++
		if old.RunID == evt.RunID && old.Type == evt.Type {
			duplicate = true
		}
	}
	if !duplicate {
		terminalCount++
	}
	if terminalCount > capacity {
		drop := terminalCount - capacity
		for i := 0; i < len(queued) && drop > 0; i++ {
			if !keepTerminal[i] {
				continue
			}
			keepTerminal[i] = false
			drop--
			if logger != nil {
				logger.Printf("actor: evicting oldest terminal event type=%s run_id=%s to keep newer terminal type=%s run_id=%s", queued[i].Type, queued[i].RunID, evt.Type, evt.RunID)
			}
		}
	}

	terminalSlots := 0
	for _, keep := range keepTerminal {
		if keep {
			terminalSlots++
		}
	}
	if !duplicate && terminalSlots < capacity {
		terminalSlots++
	}

	keepNonTerminal := capacity - terminalSlots
	if keepNonTerminal < 0 {
		keepNonTerminal = 0
	}
	keepTail := make([]bool, len(queued))
	for i := len(queued) - 1; i >= 0 && keepNonTerminal > 0; i-- {
		if IsTerminal(queued[i]) {
			continue
		}
		keepTail[i] = true
		keepNonTerminal--
	}

	for i, old := range queued {
		if IsTerminal(old) {
			if !keepTerminal[i] {
				continue
			}
		} else if !keepTail[i] {
			if logger != nil {
				logger.Printf("actor: evicting event type=%s to deliver terminal event type=%s", old.Type, evt.Type)
			}
			continue
		}
		select {
		case sub.events <- old:
		default:
			if logger != nil {
				logger.Printf("actor: dropping replayed event type=%s while rebuilding terminal queue", old.Type)
			}
		}
	}
	if duplicate {
		if logger != nil {
			logger.Printf("actor: dropping duplicate terminal event type=%s run_id=%s", evt.Type, evt.RunID)
		}
		return
	}
	select {
	case sub.events <- evt:
	default:
		if logger != nil {
			logger.Printf("actor: dropping terminal event type=%s run_id=%s because subscriber queue remained full", evt.Type, evt.RunID)
		}
	}
}

// removeAndClose drops a subscription from the fan-out list and closes it.
func (s *EventStream) removeAndClose(target *Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subs {
		if sub == target {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			close(sub.events)
			break
		}
	}
}

// Close terminates all subscribers.
func (s *EventStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, sub := range s.subs {
		sub.close()
	}
	s.subs = nil
}

// Subscription is a single consumer's view of the event stream.
type Subscription struct {
	events chan events.Event
	stream *EventStream
	once   sync.Once
	mu     sync.Mutex
}

// Events returns the read-only channel.
func (s *Subscription) Events() <-chan events.Event { return s.events }

// Close unregisters and closes the underlying channel. Idempotent.
func (s *Subscription) Close() {
	s.once.Do(func() {
		if s.stream != nil {
			s.stream.removeAndClose(s)
			return
		}
		close(s.events)
	})
}

// close is the internal version that skips the remove() call (used by
// EventStream.Close which already holds the slice under the lock).
func (s *Subscription) close() {
	s.once.Do(func() {
		close(s.events)
	})
}

// devZero is a minimal io.Writer that discards output, used to back
// the default logger without pulling in ioutil/iolog.
type devZero struct{}

func (devZero) Write(p []byte) (int, error) { return len(p), nil }

const defaultSubscriberBuffer = 64
