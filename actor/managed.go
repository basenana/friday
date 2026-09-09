package actor

import (
	"context"
	"sync/atomic"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus/bridge"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/setup"
)

// managedActor is the Registry's per-session entry. It pairs the
// core/actor Actor with the setup.AgentContext that must be closed on
// eviction, tracks activity for the idle sweep (via the
// TurnLifecycle callbacks), and owns the bus bridges that connect the
// actor to the event bus.
type managedActor struct {
	actor      *coreactor.Actor
	agentCtx   *setup.AgentContext
	stopLoop   context.CancelFunc
	inBridge   *bridge.InBridge
	outBridge  *bridge.OutBridge
	hasBridges bool

	stopped    atomic.Bool
	lastActive atomic.Int64 // UnixNano
}

// attach connects the actor to the bus: the InBridge feeds the actor
// from inbox/preempt topics, the OutBridge forwards its event stream.
func (e *managedActor) attach(b *eventbus.Bus, sessionID string) {
	e.inBridge = bridge.NewInBridge(b, sessionID, e.actor)
	e.outBridge = bridge.NewOutBridge(b, sessionID, e.actor)
	e.hasBridges = true
}

// touch records turn activity for the idle sweep.
func (e *managedActor) touch() { e.lastActive.Store(time.Now().UnixNano()) }

// close shuts the actor down (gracefully, bounded by grace) and then
// releases the agent context. Bridge teardown order matters: the
// InBridge closes first so no new input races the shutdown; the actor
// then drains its final turn with the OutBridge still forwarding
// terminal events; only afterwards does the OutBridge unsubscribe.
// Idempotent.
func (e *managedActor) close(grace time.Duration) {
	if e.stopped.Swap(true) {
		return
	}
	if e.hasBridges {
		e.inBridge.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	_ = e.actor.Shutdown(ctx)
	if e.hasBridges {
		e.outBridge.Close()
	}
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
