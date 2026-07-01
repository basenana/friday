package actor

import (
	"sync"
	"sync/atomic"
)

const defaultSubscriberBuffer = 64

type subscriptionRequest struct {
	id uint64
	ch chan Event
}

// sessionPubSub multiplexes one actor event stream to zero or more API-layer
// subscribers. It preserves event order for each subscriber and owns channel
// close on unsubscribe / actor shutdown.
type sessionPubSub struct {
	subscribeCh   chan subscriptionRequest
	unsubscribeCh chan uint64
	stopCh        chan struct{}
	doneCh        chan struct{}

	stopOnce sync.Once
	nextID   atomic.Uint64
}

func newSessionPubSub() *sessionPubSub {
	return &sessionPubSub{
		subscribeCh:   make(chan subscriptionRequest),
		unsubscribeCh: make(chan uint64),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

func (ps *sessionPubSub) subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}

	id := ps.nextID.Add(1)
	ch := make(chan Event, buffer)

	select {
	case ps.subscribeCh <- subscriptionRequest{id: id, ch: ch}:
	case <-ps.doneCh:
		close(ch)
	}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			select {
			case ps.unsubscribeCh <- id:
			case <-ps.doneCh:
			}
		})
	}

	return ch, unsubscribe
}

func (ps *sessionPubSub) stop() {
	ps.stopOnce.Do(func() {
		close(ps.stopCh)
	})
	<-ps.doneCh
}

func (ps *sessionPubSub) run(events <-chan Event, onEvent func(Event)) {
	defer close(ps.doneCh)

	subscribers := make(map[uint64]chan Event)
	closeAll := func() {
		for id, ch := range subscribers {
			close(ch)
			delete(subscribers, id)
		}
	}

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				closeAll()
				return
			}

			if onEvent != nil {
				onEvent(evt)
			}

			for id, ch := range subscribers {
			deliver:
				for {
					select {
					case ch <- evt:
						break deliver

					case req := <-ps.subscribeCh:
						subscribers[req.id] = req.ch

					case unsubID := <-ps.unsubscribeCh:
						if sub, ok := subscribers[unsubID]; ok {
							close(sub)
							delete(subscribers, unsubID)
						}
						if unsubID == id {
							break deliver
						}

					case <-ps.stopCh:
						closeAll()
						return
					}
				}
			}

		case req := <-ps.subscribeCh:
			subscribers[req.id] = req.ch

		case id := <-ps.unsubscribeCh:
			if ch, ok := subscribers[id]; ok {
				close(ch)
				delete(subscribers, id)
			}

		case <-ps.stopCh:
			closeAll()
			return
		}
	}
}
