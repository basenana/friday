// Package actor wraps a core agents.Agent into an async, event-driven
// actor with an inbox (multi-message coalescing), a structured AG-UI
// event stream, and three card/form tools.
//
// The actor package does not modify the core package. It only consumes
// the public interfaces exported by core (agents.Agent, session.Session,
// api.Request/Response, types.Delta/Event, tools.Tool).
package actor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/actor/sink"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	coretools "github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/tracing"
	"github.com/basenana/friday/core/types"
)

// ErrUnknownForm is returned by ResolveForm when no waiter exists.
var ErrUnknownForm = errors.New("actor: unknown form id")

// ErrActorStopped indicates the actor loop has terminated.
var ErrActorStopped = errors.New("actor: stopped")

// Actor wraps a core agents.Agent with an async inbox and a structured
// event stream. A single Actor is goroutine-safe after Start; inbox
// sends, subscriptions, and form submissions may proceed concurrently.
type Actor struct {
	id      string
	agent   agents.Agent
	session *session.Session

	inbox  *Inbox
	stream *EventStream
	sink   sink.EventSink

	// tools are the three card tools exposed to the agent.
	cardTools []*coretools.Tool

	// pendingForms maps formID to its waiting channel.
	pendingForms   map[string]chan FormOutcome
	pendingFormsMu sync.Mutex

	// currentRunID is the active turn id (read by event emitters).
	currentRunID atomic.Value

	cancel  context.CancelFunc
	stopped atomic.Bool
	wg      sync.WaitGroup

	filePathValidator       FilePathValidator
	richPathValidator       FilePathValidator
	diffSourcePathValidator FilePathValidator
	unifiedDiffValidator    func([]byte) error

	turnLifecycle   TurnLifecycle
	turnIDGenerator func() string
	turnTimeout     time.Duration
	extraTools      []*coretools.Tool

	turnMu     sync.Mutex
	turnCtx    context.Context
	turnCancel context.CancelFunc
}

// New constructs an Actor wrapping the given agent + session.
func New(agent agents.Agent, sess *session.Session, opts ...Option) *Actor {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	a := &Actor{
		id:                      globalIDGenerator.Next("actor"),
		agent:                   agent,
		session:                 sess,
		inbox:                   NewInbox(o.inboxBuffer, o.preemptBuffer),
		stream:                  NewEventStream(o.logger),
		sink:                    o.sink,
		pendingForms:            make(map[string]chan FormOutcome),
		filePathValidator:       o.filePathValidator,
		richPathValidator:       o.richPathValidator,
		diffSourcePathValidator: o.diffSourcePathValidator,
		unifiedDiffValidator:    o.unifiedDiffValidator,
		turnLifecycle:           o.turnLifecycle,
		turnIDGenerator:         o.turnIDGenerator,
		turnTimeout:             o.turnTimeout,
		extraTools:              o.extraTools,
	}
	if a.sink == nil {
		a.sink = sink.Nop()
	}
	a.cardTools = []*coretools.Tool{
		makeEmitCardTool(a),
		makeRequestFormTool(a),
		makeUpdateCardTool(a),
	}
	return a
}

// ID returns the actor instance id.
func (a *Actor) ID() string { return a.id }

// Tools returns the card tools. Callers attach these to the api.Request
// when driving the underlying agent directly; in normal use the actor
// attaches them itself inside the run loop.
func (a *Actor) Tools() []*coretools.Tool { return a.cardTools }

// Send enqueues a normal inbox message.
func (a *Actor) Send(ctx context.Context, msg Message) error {
	if a.stopped.Load() {
		return ErrActorStopped
	}
	if err := a.inbox.Send(ctx, msg); err != nil {
		if errors.Is(err, errInboxClosed) {
			return ErrActorStopped
		}
		return err
	}
	return nil
}

// TrySend enqueues a normal inbox message without blocking. Returns
// false when the buffer is full or the actor is stopped.
func (a *Actor) TrySend(msg Message) bool {
	if a.stopped.Load() {
		return false
	}
	return a.inbox.TrySend(msg)
}

// SendPreempt enqueues a preemption message.
func (a *Actor) SendPreempt(ctx context.Context, reason string) error {
	return a.SendPreemptScope(ctx, reason, PreemptAll)
}

// SendPreemptScope interrupts the active turn. PreemptCurrent preserves
// already queued normal messages; the default PreemptAll retains the original
// cancel-and-drain behavior used by non-interactive clients.
func (a *Actor) SendPreemptScope(ctx context.Context, reason string, scope PreemptScope) error {
	if a.stopped.Load() {
		return ErrActorStopped
	}
	cancel := a.snapshotTurnCancel()
	if err := a.inbox.SendPreempt(ctx, PreemptMessage{Reason: reason, Scope: scope}); err != nil {
		if errors.Is(err, errInboxClosed) {
			return ErrActorStopped
		}
		return err
	}
	a.preemptCapturedTurn(cancel)
	return nil
}

// SendSteer interrupts the current provider stream and schedules msg ahead of
// the normal inbox. Normal queued turns are deliberately retained.
func (a *Actor) SendSteer(ctx context.Context, msg UserTextMessage) error {
	if a.stopped.Load() {
		return ErrActorStopped
	}
	msg.Delivery = "steer"
	cancel := a.snapshotTurnCancel()
	if err := a.inbox.SendSteer(ctx, SteerMessage{UserTextMessage: msg}); err != nil {
		if errors.Is(err, errInboxClosed) {
			return ErrActorStopped
		}
		return err
	}
	a.preemptCapturedTurn(cancel)
	return nil
}

// SubmitForm delivers values for a pending form, unblocking the
// corresponding request_form tool invocation.
func (a *Actor) SubmitForm(formID string, values map[string]any) error {
	return a.ResolveForm(formID, FormOutcome{Values: values})
}

// CancelForm cancels a pending form.
func (a *Actor) CancelForm(formID string) error {
	return a.ResolveForm(formID, FormOutcome{Cancelled: true})
}

// ResolveForm satisfies the cardEmitter contract. It is also the
// backing implementation for SubmitForm / CancelForm.
func (a *Actor) ResolveForm(formID string, outcome FormOutcome) error {
	a.pendingFormsMu.Lock()
	ch, ok := a.pendingForms[formID]
	if ok {
		delete(a.pendingForms, formID)
	}
	a.pendingFormsMu.Unlock()
	if !ok {
		return ErrUnknownForm
	}
	select {
	case ch <- outcome:
	default:
		// channel has buffer 1; if full, the waiter already exited.
	}
	return nil
}

// WaitForForm blocks until a matching ResolveForm call arrives or ctx
// expires. Used by the request_form tool handler.
func (a *Actor) WaitForForm(ctx context.Context, formID string) (FormOutcome, error) {
	ch := a.prepareFormWait(formID)
	return a.waitForRegisteredForm(ctx, formID, ch)
}

func (a *Actor) prepareFormWait(formID string) chan FormOutcome {
	ch := make(chan FormOutcome, 1)
	a.pendingFormsMu.Lock()
	a.pendingForms[formID] = ch
	a.pendingFormsMu.Unlock()
	return ch
}

func (a *Actor) waitForRegisteredForm(ctx context.Context, formID string, ch chan FormOutcome) (FormOutcome, error) {
	// Cleanup on exit to avoid leaking entries.
	defer func() {
		a.pendingFormsMu.Lock()
		if current, ok := a.pendingForms[formID]; ok && current == ch {
			delete(a.pendingForms, formID)
		}
		a.pendingFormsMu.Unlock()
	}()

	for {
		select {
		case outcome := <-ch:
			return outcome, nil
		default:
		}

		select {
		case outcome := <-ch:
			return outcome, nil
		case <-ctx.Done():
			select {
			case outcome := <-ch:
				return outcome, nil
			default:
				return FormOutcome{}, ctx.Err()
			}
		}
	}
}

// Subscribe registers a new event stream consumer.
func (a *Actor) Subscribe() *Subscription { return a.stream.Subscribe(0) }

// Start launches the actor main loop. The returned context cancellation
// function stops the loop after the current turn finishes.
func (a *Actor) Start(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.wg.Add(1)
	go a.loop(ctx)
	return cancel
}

// Shutdown stops the actor with a deadline.
//
// Two-phase semantics:
//   - Phase 1 (graceful): mark stopped, close the inbox so the loop
//     stops accepting new messages, then wait for the current turn
//     (if any) to complete naturally.
//   - Phase 2 (forced): if ctx expires before the loop exits, cancel
//     the loop context to abort the in-flight agent.Chat call.
//
// The stream and sink are closed only after the loop has fully exited
// (in either phase). Returns nil on graceful exit, ctx.Err() when the
// deadline forced an abort.
//
// Shutdown is idempotent; concurrent callers race to perform the work
// but all of them observe the same outcome once the loop drains.
func (a *Actor) Shutdown(ctx context.Context) error {
	if a.stopped.Swap(true) {
		// Already stopped; wait for whatever in-flight shutdown owns
		// the drain to finish so callers see a consistent state.
		a.wg.Wait()
		return nil
	}

	// Close the inbox first so the loop's Wait() returns immediately
	// when idle. If the loop is mid-turn, this just prevents new
	// messages from being queued.
	a.inbox.Close()

	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	var aborted bool
	select {
	case <-done:
		// graceful exit
	case <-ctx.Done():
		// deadline reached: force-abort the in-flight turn.
		aborted = true
		if a.cancel != nil {
			a.cancel()
		}
		<-done
	}

	a.stream.Close()
	_ = a.sink.Close()

	if aborted {
		return ctx.Err()
	}
	return nil
}

// Stop cancels the loop immediately and waits for it to drain. It is
// the convenience form of Shutdown with an already-expired context.
// Use Shutdown when you need a graceful deadline (the common case for
// platform integration / idle-timeout shutdown).
func (a *Actor) Stop() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = a.Shutdown(ctx)
}

// loop is the actor main loop. It waits for inbox messages, drains
// anything else that has arrived, coalesces them into a single turn,
// and drives the underlying agent.
func (a *Actor) loop(ctx context.Context) {
	defer a.wg.Done()
	for {
		// Stop fast when Shutdown has been requested.
		if a.stopped.Load() {
			return
		}
		// Honor preemption first.
		if preempt, ok := a.inbox.PollPreempt(); ok {
			if preempt.Scope != PreemptCurrent {
				// Preserve the historical cancel-all behavior.
				_, _ = a.inbox.Drain(false)
				a.inbox.DrainSteers()
			}
			continue
		}
		if steer, ok := a.inbox.PollSteer(); ok {
			a.runSteer(ctx, steer)
			continue
		}

		first, ok := a.inbox.Wait(ctx)
		if !ok {
			// Inbox closed or context cancelled.
			return
		}
		if ctx.Err() != nil {
			return
		}
		switch control := first.(type) {
		case PreemptMessage:
			if control.Scope != PreemptCurrent {
				_, _ = a.inbox.Drain(false)
				a.inbox.DrainSteers()
			}
			continue
		case SteerMessage:
			a.runSteer(ctx, control)
			continue
		}

		// Drain remaining buffered messages. Form submit/cancel are
		// extracted and routed to pendingForms instead of coalesced.
		batch, forms := a.inbox.Drain(true)
		if !a.routeFormMessageIfNeeded(first) {
			batch = append([]Message{first}, batch...)
		}
		for _, m := range forms {
			a.routeFormMessage(m)
		}

		for _, turnBatch := range a.splitTurnBatches(batch) {
			bctx := a.coalesceBatch(turnBatch)
			if bctx.text == "" && len(bctx.images) == 0 {
				// No promptable content to send; nothing to do for this batch.
				continue
			}
			a.runTurn(ctx, bctx, len(turnBatch))
		}
	}
}

// batchContext carries the data extracted from a coalesced inbox
// batch that the turn needs to know about: the merged user message,
// the externally-provided turn id (if any), and the union of image
// content across messages.
type batchContext struct {
	text          string
	turnID        string
	delivery      string
	steerSequence uint64
	images        []types.ImageContent
	metadata      []map[string]any
}

func (a *Actor) runSteer(ctx context.Context, steer SteerMessage) {
	if !a.inbox.IsLatestSteer(steer) {
		return
	}
	bctx := a.coalesceBatch([]Message{steer.UserTextMessage})
	bctx.steerSequence = steer.sequence
	a.runTurn(ctx, bctx, 1)
}

// splitTurnBatches preserves the inbox drain order while preventing
// messages with explicit external turn ids from being silently merged
// into unrelated turns. Contiguous untagged messages still coalesce.
func (a *Actor) splitTurnBatches(batch []Message) [][]Message {
	if len(batch) == 0 {
		return nil
	}

	var (
		out           [][]Message
		current       []Message
		currentTurnID string
		currentTagged bool
	)

	flush := func() {
		if len(current) == 0 {
			return
		}
		group := make([]Message, len(current))
		copy(group, current)
		out = append(out, group)
		current = nil
		currentTurnID = ""
		currentTagged = false
	}

	for _, m := range batch {
		user, ok := m.(UserTextMessage)
		if !ok {
			if len(current) > 0 {
				current = append(current, m)
			}
			continue
		}

		nextTagged := user.TurnID != ""
		if len(current) == 0 {
			current = append(current, m)
			currentTurnID = user.TurnID
			currentTagged = nextTagged
			continue
		}

		if currentTagged {
			if !nextTagged || user.TurnID != currentTurnID {
				flush()
				current = append(current, m)
				currentTurnID = user.TurnID
				currentTagged = nextTagged
				continue
			}
		} else if nextTagged {
			flush()
			current = append(current, m)
			currentTurnID = user.TurnID
			currentTagged = true
			continue
		}

		current = append(current, m)
	}

	flush()
	return out
}

// coalesceBatch merges a batch of inbox messages into a single
// batchContext. UserTextMessage entries contribute their text,
// images, and metadata; SignalMessage entries are ignored for turn
// composition.
func (a *Actor) coalesceBatch(batch []Message) batchContext {
	var parts []string
	var images []types.ImageContent
	var metadata []map[string]any
	var turnID string
	var delivery string
	for _, m := range batch {
		switch v := m.(type) {
		case UserTextMessage:
			if v.Text != "" {
				parts = append(parts, v.Text)
			}
			if v.TurnID != "" && turnID == "" {
				turnID = v.TurnID
			}
			if v.Delivery != "" {
				delivery = v.Delivery
			}
			if len(v.Images) > 0 {
				images = append(images, v.Images...)
			}
			if len(v.Metadata) > 0 {
				metadata = append(metadata, cloneMetadata(v.Metadata))
			}
		case SignalMessage:
			// Signals do not contribute to the prompt in the MVP.
		}
	}
	text := strings.Join(parts, "\n\n")
	return batchContext{text: text, turnID: turnID, delivery: delivery, images: images, metadata: metadata}
}

// routeFormMessage dispatches a FormSubmitMessage / FormCancelMessage
// extracted from the inbox to the corresponding waiter.
func (a *Actor) routeFormMessage(m Message) {
	switch v := m.(type) {
	case FormSubmitMessage:
		_ = a.SubmitForm(v.FormID, v.Values)
	case FormCancelMessage:
		_ = a.CancelForm(v.FormID)
	}
}

func (a *Actor) routeFormMessageIfNeeded(m Message) bool {
	switch m.(type) {
	case FormSubmitMessage, FormCancelMessage:
		a.routeFormMessage(m)
		return true
	default:
		return false
	}
}

// runTurn drives a single agent.Chat call and pumps its outputs into
// the event stream. The lifecycle is invoked around the agent call so
// the embedding runtime can persist turn state and events.
func (a *Actor) runTurn(ctx context.Context, bctx batchContext, batchSize int) {
	turnCtx, cancel := a.deriveTurnCtx(ctx)
	a.turnMu.Lock()
	a.turnCtx = turnCtx
	a.turnCancel = cancel
	a.turnMu.Unlock()
	defer func() {
		cancel()
		a.turnMu.Lock()
		a.turnCtx = nil
		a.turnCancel = nil
		a.turnMu.Unlock()
	}()
	if bctx.steerSequence != 0 && !a.inbox.IsLatestSteer(SteerMessage{sequence: bctx.steerSequence}) {
		return
	}

	runID := bctx.turnID
	if runID == "" {
		runID = a.generateRunID()
	}
	a.currentRunID.Store(runID)

	// Start a root span for this turn so every downstream span
	// (BeforeAgent hooks like hook.mcp.before_agent, agent.react.chat,
	// tools.invoke, llm.anthropic.completion, ...) shares one trace.
	// Without this, the actor's loop runs on context.Background()
	// (registry.go:327) and each downstream span becomes its own root.
	turnCtx, turnSpan := tracing.Start(turnCtx, "actor.turn",
		tracing.WithAttributes(
			tracing.String("actor.id", a.id),
			tracing.String("turn.run_id", runID),
			tracing.String("session.id", a.session.ID),
		),
	)
	defer turnSpan.End()

	startInfo := TurnStartInfo{
		RunID:     runID,
		BatchSize: batchSize,
		Preview:   firstLine(bctx.text),
		Metadata:  bctx.metadata,
	}
	if err := a.turnLifecycle.OnTurnStart(turnCtx, startInfo); err != nil {
		// Surface lifecycle start failure as a turn error and abort.
		a.publish(turnCtx, events.NewEvent(events.KindRunError, runID).
			WithPayload(events.RunErrorData{Message: err.Error()}))
		_ = a.turnLifecycle.OnTurnComplete(turnCtx, runID, TurnOutcome{Err: err})
		return
	}

	// RUN_STARTED.
	a.publish(turnCtx, events.NewEvent(events.KindRunStarted, runID).
		WithPayload(events.RunStartedData{
			ActorID: a.id,
			Batch:   batchSize,
			Preview: startInfo.Preview,
		}))
	// Persist the complete input immediately after the public lifecycle start;
	// RUN_STARTED remains the first event for compatibility.
	a.publish(turnCtx, events.NewEvent(events.KindCustom, runID).
		WithName(events.CustomInputAccepted).
		WithPayload(events.InputAcceptedBody{
			TurnID: runID, Text: bctx.text, Delivery: bctx.delivery,
		}))

	req := &api.Request{
		Session:     a.session,
		UserMessage: bctx.text,
		Tools:       a.assembleRequestTools(),
	}
	for _, metadata := range bctx.metadata {
		for key, value := range metadata {
			if stringValue, ok := value.(string); ok {
				if req.Metadata == nil {
					req.Metadata = make(map[string]string)
				}
				req.Metadata[key] = stringValue
			}
		}
	}
	if len(bctx.images) > 0 {
		req.Images = bctx.images
	}

	resp := a.agent.Chat(turnCtx, req)
	streamErr := a.pumpResponse(turnCtx, runID, resp)

	// Finalize hook runs after the stream is fully consumed but before
	// the terminal event.
	finalizeErr := a.turnLifecycle.OnTurnFinalize(turnCtx, runID)

	// Compute the outcome BEFORE publishing RUN_FINISHED so the terminal
	// event carries the authoritative stop reason.
	outcome := TurnOutcome{}
	if streamErr != nil {
		outcome.Err = streamErr
	}
	if finalizeErr != nil {
		if outcome.Err != nil {
			outcome.Err = errors.Join(outcome.Err, finalizeErr)
		} else {
			outcome.Err = finalizeErr
		}
	}
	outcome.Cancelled = errors.Is(outcome.Err, context.Canceled) ||
		errors.Is(turnCtx.Err(), context.Canceled)

	stopReason := "end_turn"
	switch {
	case outcome.Cancelled:
		stopReason = "cancelled"
	case outcome.Err != nil:
		stopReason = "error"
	}

	// RUN_FINISHED carries any open interrupts plus the stop reason.
	a.publish(turnCtx, events.NewEvent(events.KindRunFinished, runID).
		WithPayload(events.RunFinishedData{
			Interrupts: a.openInterrupts(),
			StopReason: stopReason,
		}))

	_ = a.turnLifecycle.OnTurnComplete(turnCtx, runID, outcome)
}

// deriveTurnCtx wraps the loop context with a per-turn cancel. When
// the actor was configured with a turn timeout, the deadline is also
// applied.
func (a *Actor) deriveTurnCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if a.turnTimeout > 0 {
		return context.WithTimeout(parent, a.turnTimeout)
	}
	return context.WithCancel(parent)
}

func (a *Actor) currentTurnContext() context.Context {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	return a.turnCtx
}

// generateRunID produces a run id using the configured generator, or
// falls back to the actor's built-in id generator when none was set.
func (a *Actor) generateRunID() string {
	if a.turnIDGenerator != nil {
		return a.turnIDGenerator()
	}
	return globalIDGenerator.Next("run")
}

// assembleRequestTools returns the full tool set passed to each Chat
// call: the actor's three card tools followed by any injected domain
// tools.
func (a *Actor) assembleRequestTools() []*coretools.Tool {
	if len(a.extraTools) == 0 {
		return a.cardTools
	}
	out := make([]*coretools.Tool, 0, len(a.cardTools)+len(a.extraTools))
	out = append(out, a.cardTools...)
	out = append(out, a.extraTools...)
	return out
}

// responseError resolves the terminal error for a response without
// blocking forever when the turn was cancelled before the agent
// closed its response channels.
func (a *Actor) responseError(ctx context.Context, resp *api.Response) error {
	select {
	case err, ok := <-resp.Error():
		if ok && err != nil {
			return err
		}
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// pumpResponse fans out two goroutines reading the LLM delta stream
// and the core session event stream, then waits for both to complete.
// The delta pump is authoritative: when resp closes, it signals the
// event pump to exit (the session never closes subscriber channels).
func (a *Actor) pumpResponse(ctx context.Context, runID string, resp *api.Response) error {
	translator := NewTranslator(runID)
	publish := func(evt events.Event) { a.publish(ctx, evt) }

	// Subscribe to core session events BEFORE we begin consuming the
	// response, so we do not miss tool.start etc. The unsubscribe
	// function is invoked after the pump finishes.
	coreCh, unsub := a.session.SubscribeEvents()
	defer unsub()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		translator.pumpDeltas(ctx, resp.Deltas(), publish)
		close(done)
	}()
	go func() {
		defer wg.Done()
		translator.pumpEvents(ctx, coreCh, publish, done)
	}()

	wg.Wait()

	if err := a.responseError(ctx, resp); err != nil {
		a.publish(ctx, events.NewEvent(events.KindRunError, runID).
			WithPayload(events.RunErrorData{Message: err.Error()}))
		return err
	}
	return nil
}

// publish emits an event to subscribers and the sink. It also forwards
// the event to the turn lifecycle so the embedding runtime can persist
// it. The lifecycle callback is invoked from the publishing goroutine,
// which is the turn goroutine or a card/form tool handler.
func (a *Actor) publish(ctx context.Context, evt events.Event) {
	if ctx == nil {
		ctx = context.Background()
	}
	evt.ActorID = a.id
	a.stream.Publish(evt)
	// Append to sink best-effort; sink errors are not surfaced to the
	// actor's callers in the MVP.
	_ = a.sink.Append(context.Background(), evt)
	// Forward to the lifecycle implementation. Use evt.RunID as the
	// runID argument (the event carries it for non-CUSTOM events; for
	// CUSTOM events emitted by card/form tools it is also set via
	// EmitCustom).
	a.turnLifecycle.OnTurnEvent(ctx, evt.RunID, evt)
}

// openInterrupts returns the currently pending form interrupts.
func (a *Actor) openInterrupts() []events.Interrupt {
	a.pendingFormsMu.Lock()
	defer a.pendingFormsMu.Unlock()
	out := make([]events.Interrupt, 0, len(a.pendingForms))
	for id := range a.pendingForms {
		out = append(out, events.Interrupt{Type: "form", ID: id, Name: events.CustomFormRequested})
	}
	return out
}

func (a *Actor) snapshotTurnCancel() context.CancelFunc {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	return a.turnCancel
}

func (a *Actor) preemptCapturedTurn(cancel context.CancelFunc) {
	cancelledForms := a.cancelPendingForms()
	for _, formID := range cancelledForms {
		a.EmitCustom(events.CustomFormCancelled, formID, events.FormCancelledBody{FormID: formID})
	}
	if cancel != nil {
		cancel()
	}
}

func (a *Actor) cancelPendingForms() []string {
	a.pendingFormsMu.Lock()
	if len(a.pendingForms) == 0 {
		a.pendingFormsMu.Unlock()
		return nil
	}
	formIDs := make([]string, 0, len(a.pendingForms))
	waiters := make([]chan FormOutcome, 0, len(a.pendingForms))
	for id, ch := range a.pendingForms {
		delete(a.pendingForms, id)
		formIDs = append(formIDs, id)
		waiters = append(waiters, ch)
	}
	a.pendingFormsMu.Unlock()

	for _, ch := range waiters {
		select {
		case ch <- FormOutcome{Cancelled: true}:
		default:
		}
	}
	return formIDs
}

// firstLine returns the first line of s, truncated for preview use.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// GoString prevents accidental printing of internal fields.
func (a *Actor) GoString() string {
	return fmt.Sprintf("*actor.Actor{id:%s stopped:%v}", a.id, a.stopped.Load())
}
