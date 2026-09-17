package bridge

import (
	"sync"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
)

// OutBridge forwards an actor's event stream to the bus, translating each
// event to its topic via RouteEvent. The actor assigns Seq before this bridge
// so consumers can detect loss in both the actor stream and downstream bus.
type OutBridge struct {
	bus     *eventbus.Bus
	session string
	actor   *coreactor.Actor
	sub     *coreactor.Subscription
	tracker *ToolCallTracker

	done chan struct{}
	once sync.Once
}

// NewOutBridge subscribes the actor's event stream and starts
// forwarding.
func NewOutBridge(b *eventbus.Bus, sessionID string, a *coreactor.Actor) *OutBridge {
	ob := &OutBridge{
		bus:     b,
		session: sessionID,
		actor:   a,
		tracker: NewToolCallTracker(),
		done:    make(chan struct{}),
	}
	ob.sub = a.Subscribe()
	go ob.loop()
	return ob
}

func (o *OutBridge) loop() {
	defer close(o.done)
	for evt := range o.sub.Events() {
		topic, ok := RouteEvent(o.session, evt, o.tracker)
		if !ok {
			continue
		}
		o.bus.Publish(topic, bus.Envelope{
			Event:   evt,
			Topic:   topic,
			Session: o.session,
			ActorID: evt.ActorID,
			From:    "actor",
			Seq:     uint64(evt.Seq),
			TS:      bus.NextTS(),
		})
	}
}

// Close unsubscribes from the actor stream and waits until every
// already-published event has been forwarded.
func (o *OutBridge) Close() {
	o.once.Do(func() {
		o.sub.Close()
		<-o.done
	})
}
