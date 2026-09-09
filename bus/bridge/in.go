package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
)

// InBridge feeds the actor from the bus: inbox envelopes become actor
// messages (user text / form submit / form cancel), preempt envelopes
// become preemptions. Inbox and preempt use separate serial listeners and
// internal queues so cancellation remains responsive when normal input is
// saturated; one goroutine drives the actor and prioritizes preemption.
//
// Delivery failures (actor stopped, inbox closed) are reported on the
// status topic as inbox_dropped envelopes echoing the sender identity.
type InBridge struct {
	bus     *eventbus.Bus
	session string
	actor   *coreactor.Actor

	ids      []string
	inbox    chan bus.Envelope
	preempts chan bus.Envelope
	ctx      context.Context
	cancel   context.CancelFunc
	quit     chan struct{}
	closed   atomic.Bool
	done     chan struct{}
	once     sync.Once
}

// NewInBridge subscribes the inbox and preempt topics and starts the
// forwarding loop.
func NewInBridge(b *eventbus.Bus, sessionID string, a *coreactor.Actor) *InBridge {
	ctx, cancel := context.WithCancel(context.Background())
	ib := &InBridge{
		bus:      b,
		session:  sessionID,
		actor:    a,
		inbox:    make(chan bus.Envelope, 64),
		preempts: make(chan bus.Envelope, 8),
		ctx:      ctx,
		cancel:   cancel,
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	inboxHandler := func(env bus.Envelope) {
		select {
		case ib.inbox <- env:
		case <-ib.quit:
		}
	}
	preemptHandler := func(env bus.Envelope) {
		select {
		case ib.preempts <- env:
		case <-ib.quit:
		}
	}
	ib.ids = []string{
		b.SubscribeSerial([]string{bus.TopicInbox(sessionID)}, inboxHandler,
			eventbus.SerialConfig{Overflow: eventbus.OverflowBlock}),
		b.SubscribeSerial([]string{bus.TopicPreempt(sessionID)}, preemptHandler,
			eventbus.SerialConfig{Overflow: eventbus.OverflowBlock}),
	}
	go ib.loop()
	return ib
}

func (ib *InBridge) loop() {
	defer close(ib.done)
	for {
		// Give cancellation its own high-priority lane so a saturated normal
		// inbox cannot keep a preempt request behind user messages.
		select {
		case env := <-ib.preempts:
			ib.dispatchPreempt(env)
			continue
		default:
		}

		select {
		case env := <-ib.preempts:
			ib.dispatchPreempt(env)
		case env := <-ib.inbox:
			ib.dispatchInbox(env)
		case <-ib.quit:
			return
		}
	}
}

func (ib *InBridge) dispatchPreempt(env bus.Envelope) {
	if ib.closed.Load() {
		return
	}
	var in bus.PreemptInput
	if err := events.DecodePayload(env.Event, &in); err != nil {
		return
	}
	// A bridge-scoped context guarantees Close can unblock a saturated
	// preempt inbox. Failure is otherwise benign: the actor is already
	// stopped or the turn has already ended.
	_ = ib.actor.SendPreempt(ib.ctx, in.Reason)
}

func (ib *InBridge) dispatchInbox(env bus.Envelope) {
	if ib.closed.Load() {
		return
	}

	var msg coreactor.Message
	switch env.Name {
	case bus.InboxUserText:
		var in bus.UserTextInput
		if err := events.DecodePayload(env.Event, &in); err != nil {
			ib.reportDrop(env, "bad payload: "+err.Error())
			return
		}
		msg = coreactor.UserTextMessage{
			Text:     in.Text,
			TurnID:   in.TurnID,
			Metadata: in.Metadata,
		}
	case bus.InboxFormSubmit:
		var in bus.FormSubmitInput
		if err := events.DecodePayload(env.Event, &in); err != nil {
			ib.reportDrop(env, "bad payload: "+err.Error())
			return
		}
		msg = coreactor.FormSubmitMessage{
			FormID: in.FormID,
			Values: in.Values,
		}
	case bus.InboxFormCancel:
		var in bus.FormCancelInput
		if err := events.DecodePayload(env.Event, &in); err != nil {
			ib.reportDrop(env, "bad payload: "+err.Error())
			return
		}
		msg = coreactor.FormCancelMessage{FormID: in.FormID}
	default:
		ib.reportDrop(env, "unknown inbox kind: "+env.Name)
		return
	}
	if !ib.actor.TrySend(msg) {
		ib.reportDrop(env, "actor inbox full or stopped")
	}
}

func (ib *InBridge) reportDrop(env bus.Envelope, reason string) {
	drop := events.NewEvent(events.KindCustom, "").WithName("status." + bus.StatusInboxDropped)
	drop = drop.WithPayload(bus.InboxDropped{
		From:   env.From,
		TurnID: turnIDOf(env),
		Reason: reason,
	})
	ib.bus.Publish(bus.TopicStatus(ib.session, bus.StatusInboxDropped), bus.Envelope{
		Event:   drop,
		Topic:   bus.TopicStatus(ib.session, bus.StatusInboxDropped),
		Session: ib.session,
		From:    "actor",
		TS:      bus.NextTS(),
	})
}

func turnIDOf(env bus.Envelope) string {
	var in bus.UserTextInput
	if events.DecodePayload(env.Event, &in) == nil {
		return in.TurnID
	}
	return ""
}

// Close stops accepting new bus messages, cancels blocked preempt delivery,
// and waits for the forwarding loop to exit. It must be called before the
// actor is shut down so no in-flight inbox message races the shutdown.
func (ib *InBridge) Close() {
	ib.once.Do(func() {
		ib.closed.Store(true)
		ib.cancel()
		close(ib.quit)
		for _, id := range ib.ids {
			ib.bus.Unsubscribe(id)
		}
		<-ib.done
	})
}
