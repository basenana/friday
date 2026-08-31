package actor

import (
	"context"
	"errors"
	"sync"

	"github.com/basenana/friday/core/types"
)

var errInboxClosed = errors.New("actor: inbox closed")

// Message is any value accepted by the actor inbox. Concrete types are
// declared in this file. The unexported marker prevents external
// implementations.
type Message interface {
	msgMarker()
}

// Priority ranks user-bound messages. Preempt messages bypass the
// normal FIFO order.
type Priority int

const (
	PriorityNormal Priority = 0
	PriorityHigh   Priority = 1
)

// UserTextMessage is the canonical inbound user message.
//
// TurnID is optional. When non-empty the actor uses it verbatim as
// the runID for the turn it triggers, letting the embedding runtime
// correlate turn events with an externally pre-allocated identifier
// (e.g. a DB row id). When empty the actor generates one using its
// configured TurnIDGenerator (or the built-in default).
//
// Images carries ordered multimodal image content attached to the
// message. When non-empty the actor forwards them to the underlying
// agent's api.Request.Images so the model sees them.
//
// Metadata is an opaque escape hatch for per-message context that
// does not deserve a dedicated field. The actor never inspects it;
// embedding runtimes may use it to thread request-scoped data
// (attachment ids, trace ids, etc.) to lifecycle hooks.
type UserTextMessage struct {
	Text     string
	Priority Priority
	TurnID   string
	Images   []types.ImageContent
	Metadata map[string]any
}

func (UserTextMessage) msgMarker() {}

// FormSubmitMessage carries user-supplied values for a pending form.
// It is routed to the corresponding pendingForm channel by FormID,
// not coalesced into a turn batch.
type FormSubmitMessage struct {
	FormID string
	Values map[string]any
}

func (FormSubmitMessage) msgMarker() {}

// FormCancelMessage cancels a pending form.
type FormCancelMessage struct {
	FormID string
}

func (FormCancelMessage) msgMarker() {}

// SignalMessage is a side-channel signal that does not start a turn on
// its own. The actor may react to it (e.g. update internal state) but
// does not feed it to the LLM. MVP ignores unknown signals.
type SignalMessage struct {
	Name    string
	Payload any
}

func (SignalMessage) msgMarker() {}

// PreemptMessage interrupts the running turn (if any) and discards the
// current batch. It travels on a dedicated priority channel.
type PreemptMessage struct {
	Reason string
}

// Preempt messages live on a separate channel but expose the same
// Message interface for symmetry.
func (PreemptMessage) msgMarker() {}

// Inbox is the actor's message queue. It exposes a normal FIFO channel
// for ordinary messages and a separate priority channel for preemption.
type Inbox struct {
	ch       chan Message
	preemptC chan PreemptMessage

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// NewInbox builds an inbox with the given buffer sizes.
func NewInbox(buffer, preemptBuffer int) *Inbox {
	if buffer <= 0 {
		buffer = defaultInboxBuffer
	}
	if preemptBuffer <= 0 {
		preemptBuffer = defaultPreemptBuffer
	}
	return &Inbox{
		ch:       make(chan Message, buffer),
		preemptC: make(chan PreemptMessage, preemptBuffer),
		done:     make(chan struct{}),
	}
}

// Send enqueues a normal message. It blocks when the buffer is full.
// Returns ctx.Err() if the context expires first.
func (in *Inbox) Send(ctx context.Context, msg Message) error {
	if in.isClosed() {
		return errInboxClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-in.done:
		return errInboxClosed
	case in.ch <- msg:
		return nil
	}
}

// TrySend enqueues a normal message without blocking. Returns false
// (and no error) when the buffer is full.
func (in *Inbox) TrySend(msg Message) bool {
	if in.isClosed() {
		return false
	}
	select {
	case <-in.done:
		return false
	case in.ch <- msg:
		return true
	default:
		return false
	}
}

// SendPreempt enqueues a preemption message.
func (in *Inbox) SendPreempt(ctx context.Context, msg PreemptMessage) error {
	if in.isClosed() {
		return errInboxClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-in.done:
		return errInboxClosed
	case in.preemptC <- msg:
		return nil
	}
}

// PollPreempt returns a pending preemption message if one is queued,
// without blocking. The ok flag is false when none is available or
// when the inbox has been closed.
func (in *Inbox) PollPreempt() (PreemptMessage, bool) {
	select {
	case m := <-in.preemptC:
		return m, true
	default:
	}
	select {
	case <-in.done:
		return PreemptMessage{}, false
	default:
		return PreemptMessage{}, false
	}
}

// Wait blocks until at least one message is available, the context is
// cancelled, or the inbox is closed. On close, returns nil, false.
func (in *Inbox) Wait(ctx context.Context) (Message, bool) {
	select {
	case <-ctx.Done():
		return nil, false
	case <-in.done:
		return nil, false
	case m := <-in.ch:
		return m, true
	}
}

// Drain returns all currently-buffered messages without blocking. This
// is the Erlang `receive after 0` mailbox-drain pattern used by the
// Drain strategy.
//
// FilterFormMessages controls whether FormSubmit / FormCancel messages
// are extracted from the batch. When true, such messages are returned
// via the forms slice rather than the batch, since they must be routed
// to pendingForms instead of coalesced into the user prompt.
func (in *Inbox) Drain(filterFormMessages bool) (batch []Message, forms []Message) {
	for {
		select {
		case m := <-in.ch:
			if filterFormMessages {
				switch m.(type) {
				case FormSubmitMessage, FormCancelMessage:
					forms = append(forms, m)
					continue
				}
			}
			batch = append(batch, m)
		default:
			return batch, forms
		}
	}
}

// Close shuts down the inbox. Subsequent Send calls return errInboxClosed.
func (in *Inbox) Close() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return
	}
	in.closed = true
	close(in.done)
}

func (in *Inbox) isClosed() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.closed
}

const (
	defaultInboxBuffer   = 64
	defaultPreemptBuffer = 4
)
