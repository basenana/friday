package actor

import (
	"context"

	"github.com/basenana/friday/core/actor/events"
)

// TurnStartInfo carries the metadata of a turn about to be driven.
type TurnStartInfo struct {
	RunID     string
	BatchSize int
	Preview   string
	// Metadata preserves the per-message opaque metadata attached to the
	// coalesced UserTextMessage values in arrival order.
	Metadata []map[string]any
}

// TurnOutcome describes how a turn terminated. Err is non-nil on
// failure. Cancelled is true when the turn was aborted by preemption
// (user-initiated cancel, shutdown, or form cancellation cascade).
type TurnOutcome struct {
	Err       error
	Cancelled bool
}

// TurnLifecycle is the contract the actor uses to notify an external
// runtime about turn boundaries and the events emitted during a turn.
//
// The actor treats the lifecycle as best-effort: hook errors are
// surfaced to the OnTurnComplete outcome but do not stop the actor's
// main loop. Implementations are responsible for their own
// synchronization; OnTurnEvent may be invoked concurrently by the
// response delta pump, the core-event pump, and tool handlers.
//
// Call ordering per turn:
//
//  1. OnTurnStart    (before RUN_STARTED)
//  2. OnTurnEvent*   (one call per published event)
//  3. OnTurnFinalize (after the response stream is fully consumed)
//  4. OnTurnComplete (after RUN_FINISHED)
//
// OnTurnEvent is invoked with the turn context so cancellation and
// timeouts propagate into external persistence. Implementors should
// still keep it cheap (e.g. enqueue to a buffered sink).
type TurnLifecycle interface {
	OnTurnStart(ctx context.Context, info TurnStartInfo) error
	OnTurnEvent(ctx context.Context, runID string, evt events.Event)
	OnTurnFinalize(ctx context.Context, runID string) error
	OnTurnComplete(ctx context.Context, runID string, outcome TurnOutcome) error
}

// nopLifecycle is the zero-value default. It does nothing.
type nopLifecycle struct{}

func (nopLifecycle) OnTurnStart(context.Context, TurnStartInfo) error          { return nil }
func (nopLifecycle) OnTurnEvent(context.Context, string, events.Event)         {}
func (nopLifecycle) OnTurnFinalize(context.Context, string) error              { return nil }
func (nopLifecycle) OnTurnComplete(context.Context, string, TurnOutcome) error { return nil }
