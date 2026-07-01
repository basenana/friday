// Package actor provides a per-session execution entity that sits between
// transport adapters (A2A, ACP, AG-UI, ...) and the core Agent.
//
// An Actor owns an inbox channel (incoming user messages) and an outcome
// channel (outgoing AG-UI style events). It is pure: it has no dependency on
// the eventbus package. Fan-out to multiple subscribers is the adapter
// layer's responsibility — see the Registry's fanout goroutine.
package actor

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/setup"
)

// State is the lifecycle state of an Actor.
type State int32

const (
	StateIdle       State = 0
	StateProcessing State = 1
	StateShutdown   State = 2
)

// Option configures an Actor at construction time.
type Option func(*actorOptions)

type actorOptions struct {
	inboxBuffer   int
	outcomeBuffer int
}

// WithInboxBuffer sets the inbox channel buffer size (default 16).
func WithInboxBuffer(n int) Option {
	return func(o *actorOptions) { o.inboxBuffer = n }
}

// WithOutcomeBuffer sets the outcome channel buffer size (default 256).
func WithOutcomeBuffer(n int) Option {
	return func(o *actorOptions) { o.outcomeBuffer = n }
}

// Actor is a per-session concurrent execution entity.
//
// Lifecycle: Idle → (inbox message arrives) → Processing → Idle → ... → Shutdown.
// Every transition out of Processing reloads a fresh core Agent through
// setup.NewAgent so there is no cross-run state leakage; persistence is the
// session store's job (history.jsonl on disk).
type Actor struct {
	SessionID string

	inbox   chan Message
	outcome chan Event

	state      atomic.Int32
	lastActive atomic.Int64 // UnixNano
	seq        atomic.Int64

	sessMgr setup.SessionManager
	cfg     *config.Config

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// New constructs an Actor and starts its loop goroutine.
func New(sessionID string, sessMgr setup.SessionManager, cfg *config.Config, opts ...Option) *Actor {
	options := actorOptions{inboxBuffer: 16, outcomeBuffer: 256}
	for _, o := range opts {
		o(&options)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a := &Actor{
		SessionID: sessionID,
		inbox:     make(chan Message, options.inboxBuffer),
		outcome:   make(chan Event, options.outcomeBuffer),
		sessMgr:   sessMgr,
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	a.lastActive.Store(time.Now().UnixNano())
	go a.loop()
	return a
}

// Send delivers a message to the actor inbox. Non-blocking: returns false
// when the inbox is full (caller should treat as back-pressure / reject).
func (a *Actor) Send(msg Message) bool {
	select {
	case a.inbox <- msg:
		return true
	default:
		return false
	}
}

// Outcome returns the event output channel consumed by the fanout pump.
func (a *Actor) Outcome() <-chan Event { return a.outcome }

// Shutdown cancels the actor context and blocks until its loop has exited
// and the outcome channel has been closed.
func (a *Actor) Shutdown() {
	a.cancel()
	<-a.done
}

// Done is closed when the loop goroutine exits.
func (a *Actor) Done() <-chan struct{} { return a.done }

// State reports the current lifecycle state.
func (a *Actor) State() State { return State(a.state.Load()) }

// LastActive reports the last time the actor transitioned out of Processing.
func (a *Actor) LastActive() time.Time { return time.Unix(0, a.lastActive.Load()) }

// loop is the actor's main goroutine. It idles waiting for inbox messages,
// drains any backlog, runs the agent, and repeats until ctx is cancelled.
func (a *Actor) loop() {
	defer close(a.done)
	defer close(a.outcome)
	defer a.state.Store(int32(StateShutdown))

	for {
		select {
		case <-a.ctx.Done():
			return
		case msg, ok := <-a.inbox:
			if !ok {
				return
			}
			a.processMessages(msg)
		}
	}
}

// processMessages drains the inbox backlog (non-blocking) and runs the agent
// exactly once with the merged batch.
func (a *Actor) processMessages(first Message) {
	msgs := []Message{first}
	for {
		select {
		case m := <-a.inbox:
			msgs = append(msgs, m)
		default:
			goto run
		}
	}
run:
	a.runAgent(msgs)
}

// runAgent builds a fresh core Agent for the run, wires the actor hook +
// event subscriptions, calls Chat, and emits lifecycle events.
func (a *Actor) runAgent(msgs []Message) {
	a.state.Store(int32(StateProcessing))
	defer func() {
		a.state.Store(int32(StateIdle))
		a.lastActive.Store(time.Now().UnixNano())
	}()

	prompt, imageURLs := MergeMessages(msgs)
	runID := types.NewID()

	a.emit(Event{Type: EventRunStarted, RunID: runID, Data: map[string]any{
		"thread_id": a.SessionID,
		"msg_count": len(msgs),
	}})

	agentCtx, err := setup.NewAgent(a.sessMgr, a.cfg, setup.WithSessionID(a.SessionID))
	if err != nil {
		a.emit(Event{Type: EventRunError, RunID: runID, Data: map[string]any{
			"message": err.Error(),
			"code":    "setup_failed",
		}})
		a.emit(Event{Type: EventRunFinished, RunID: runID, Data: map[string]any{
			"stop_reason": "error",
		}})
		return
	}
	defer agentCtx.Close()

	// Inject actor capabilities (emit_activity tool, future state projection).
	agentCtx.Session.RegisterHook(newActorHook(a.emit, runID))

	// Bridge core session events → AG-UI events.
	coreEvents, unsubscribeEvents := agentCtx.Session.SubscribeEvents()
	defer unsubscribeEvents()

	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		a.bridgeCoreEvents(coreEvents, runID)
	}()

	var resp *api.Response
	if len(imageURLs) > 0 {
		resp = agentCtx.ChatWithImageRefs(a.ctx, prompt, imageURLs...)
	} else {
		resp = agentCtx.Chat(a.ctx, prompt)
	}

	a.bridgeResponseDeltas(resp, runID)
	<-bridgeDone

	stopReason := "end_turn"
	if a.ctx.Err() != nil {
		stopReason = "cancelled"
	}
	a.emit(Event{Type: EventRunFinished, RunID: runID, Data: map[string]any{
		"stop_reason": stopReason,
	}})
}

// bridgeCoreEvents translates core session events into AG-UI events.
// Runs in its own goroutine; exits when the core event channel is closed
// (which happens on session.Close).
func (a *Actor) bridgeCoreEvents(ch <-chan types.Event, runID string) {
	for evt := range ch {
		switch evt.Type {
		case types.EventModelStart:
			a.emit(Event{Type: EventStepStarted, RunID: runID, Data: map[string]any{
				"step_name": "llm",
			}})
		case types.EventModelFinish:
			data := map[string]any{"step_name": "llm"}
			for k, v := range evt.Data {
				data[k] = v
			}
			a.emit(Event{Type: EventStepFinished, RunID: runID, Data: data})
		case types.EventLoopStart:
			data := map[string]any{"step_name": "react_loop"}
			for k, v := range evt.Data {
				data[k] = v
			}
			a.emit(Event{Type: EventStepStarted, RunID: runID, Data: data})
		case types.EventToolStart:
			a.emit(Event{Type: EventToolCallStart, RunID: runID, Data: map[string]any{
				"tool_call_id": evt.Data["id"],
				"tool_name":    evt.Data["tool"],
				"input":        evt.Data["input"],
			}})
		case types.EventToolFinish:
			a.emit(Event{Type: EventToolCallResult, RunID: runID, Data: map[string]any{
				"tool_call_id": evt.Data["id"],
				"tool_name":    evt.Data["tool"],
				"output":       evt.Data["output"],
				"success":      evt.Data["success"],
			}})
			a.emit(Event{Type: EventToolCallEnd, RunID: runID, Data: map[string]any{
				"tool_call_id": evt.Data["id"],
			}})
		case types.EventTodoUpdate:
			raw := map[string]any{}
			for k, v := range evt.Data {
				raw[k] = v
			}
			a.emit(Event{Type: EventActivityDelta, RunID: runID, Data: map[string]any{
				"activity_type": "PLAN",
				"raw":           raw,
			}})
		case types.EventSubagentStart, types.EventSubagentFinish:
			val := map[string]any{}
			for k, v := range evt.Data {
				val[k] = v
			}
			a.emit(Event{Type: EventCustom, RunID: runID, Data: map[string]any{
				"name":  string(evt.Type),
				"value": val,
			}})
		}
	}
}

// bridgeResponseDeltas translates the streaming api.Response into AG-UI
// text/reasoning events. Runs on the runAgent goroutine (single writer for
// text-stream events, so TEXT_MESSAGE_CONTENT order is strictly preserved).
//
// Note: api.Response.Close() closes BOTH the delta and error channels. A
// closed error channel is not a terminal condition — only a real error value
// or a closed delta channel is. We nil out a closed error channel so it stops
// participating in the select (a bare `continue` would busy-loop because a
// closed channel is always ready to read).
func (a *Actor) bridgeResponseDeltas(resp *api.Response, runID string) {
	var (
		textStarted      bool
		reasoningStarted bool
		messageID        = types.NewID()
		errCh            = resp.Error()
	)

	for {
		select {
		case err, ok := <-errCh:
			if !ok {
				errCh = nil // stop monitoring; deltas still in flight
				continue
			}
			if err != nil {
				a.emit(Event{Type: EventRunError, RunID: runID, Data: map[string]any{
					"message": err.Error(),
					"code":    "stream_error",
				}})
				return
			}
		case delta, ok := <-resp.Deltas():
			if !ok {
				// delta channel closed ⇒ agent.Chat finished.
				if textStarted {
					a.emit(Event{Type: EventTextMessageEnd, RunID: runID, MessageID: messageID})
				}
				if reasoningStarted {
					a.emit(Event{Type: EventReasoningEnd, RunID: runID, MessageID: messageID})
				}
				return
			}

			if delta.Content != "" {
				if !textStarted {
					textStarted = true
					a.emit(Event{Type: EventTextMessageStart, RunID: runID, MessageID: messageID, Data: map[string]any{
						"role": "assistant",
					}})
				}
				a.emit(Event{Type: EventTextMessageContent, RunID: runID, MessageID: messageID, Data: map[string]any{
					"delta": delta.Content,
				}})
			}

			if delta.Reasoning != "" {
				if !reasoningStarted {
					reasoningStarted = true
					a.emit(Event{Type: EventReasoningStart, RunID: runID, MessageID: messageID})
				}
				a.emit(Event{Type: EventReasoningMessageContent, RunID: runID, MessageID: messageID, Data: map[string]any{
					"delta": delta.Reasoning,
				}})
			}
		}
	}
}

// emit pushes an event into the outcome channel. It stamps SessionID,
// Timestamp and a monotonically increasing Seq. Non-blocking: a full outcome
// channel means a slow consumer and the event is dropped (by design).
func (a *Actor) emit(evt Event) {
	evt.SessionID = a.SessionID
	evt.Timestamp = time.Now()
	evt.Seq = a.seq.Add(1)

	select {
	case a.outcome <- evt:
	default:
	}
}
