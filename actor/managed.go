package actor

import (
	"context"
	"sync/atomic"
	"time"

	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/setup"
)

// managedActor is the Registry's per-session entry. It pairs the
// core/actor Actor with the setup.AgentContext that must be closed on
// eviction, and tracks activity for the idle sweep (via the
// TurnLifecycle callbacks).
type managedActor struct {
	actor    *coreactor.Actor
	agentCtx *setup.AgentContext
	stopLoop context.CancelFunc

	stopped    atomic.Bool
	lastActive atomic.Int64 // UnixNano
}

// touch records turn activity for the idle sweep.
func (e *managedActor) touch() { e.lastActive.Store(time.Now().UnixNano()) }

// close shuts the actor down (gracefully, bounded by grace) and then
// releases the agent context. Idempotent.
func (e *managedActor) close(grace time.Duration) {
	if e.stopped.Swap(true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	_ = e.actor.Shutdown(ctx)
	e.agentCtx.Close()
}

// Compile-time check: managedActor tracks activity as a TurnLifecycle.
var _ coreactor.TurnLifecycle = (*managedActor)(nil)

func (e *managedActor) OnTurnStart(_ context.Context, _ coreactor.TurnStartInfo) error {
	e.touch()
	return nil
}

func (e *managedActor) OnTurnEvent(_ context.Context, _ string, _ events.Event) {
	e.touch()
}

func (e *managedActor) OnTurnFinalize(_ context.Context, _ string) error {
	return nil
}

func (e *managedActor) OnTurnComplete(_ context.Context, _ string, _ coreactor.TurnOutcome) error {
	e.touch()
	return nil
}
