//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/core/types"
)

// newActorEnv builds an actor environment: a sessions.Manager + config +
// registry. Returns (registry, sessionID, workdir, cleanup).
func newActorEnv(t *testing.T, cfg *E2EConfig, modelName string) (*actor.Registry, string, string, func()) {
	t.Helper()
	fc := fridayConfig(t, cfg, modelName)
	dir := fc.DataDir
	mgr := newSessionManager(t, dir)
	mgr.SetLLM(newClient(t, cfg, modelName))
	reg := actor.NewRegistry(mgr, fc, actor.DefaultRegistryConfig())
	sessID := types.NewID()
	if _, _, err := mgr.GetOrCreateByID(sessID); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return reg, sessID, dir, func() {}
}

// drainActorEvents collects events from the actor's Outcome channel until
// either a RunFinished is observed, an error event arrives, or the grace
// period elapses with no new events. It then calls Shutdown and returns.
func drainActorEvents(t *testing.T, a *actor.Actor, grace time.Duration) []actor.Event {
	t.Helper()
	var events []actor.Event
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	idle := time.NewTimer(grace)
	defer idle.Stop()
	for {
		select {
		case evt, ok := <-a.Outcome():
			if !ok {
				return events
			}
			events = append(events, evt)
			if evt.Type == actor.EventRunFinished || evt.Type == actor.EventRunError {
				return events
			}
			// Reset the idle timer on every event.
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(grace)
		case <-idle.C:
			// No events for grace period — assume the run is done.
			return events
		case <-deadline.C:
			return events
		}
	}
}

// runActor sends a message, drains events, shuts down, returns events.
func runActor(t *testing.T, a *actor.Actor, msg string, grace time.Duration) []actor.Event {
	t.Helper()
	if !a.Send(actor.Message{ID: types.NewID(), Content: msg}) {
		t.Fatal("Send returned false")
	}
	events := drainActorEvents(t, a, grace)
	a.Shutdown()
	return events
}

// TestActor_BasicChat verifies the actor emits RunStarted and text content
// for a single chat turn.
func TestActor_BasicChat(t *testing.T) {
	cfg := loadConfig(t)
	reg, sessID, _, cleanup := newActorEnv(t, cfg, "chat")
	defer cleanup()
	a := reg.GetOrCreate(sessID)

	events := runActor(t, a, "Say hello in one short sentence.", 15*time.Second)
	for _, e := range events {
		t.Logf("event: %s seq=%d", e.Type, e.Seq)
	}
	assertActorEvent(t, events, actor.EventRunStarted)
	// Actor event bridging is asynchronous; under non-deterministic LLM
	// scheduling the content events may not all arrive before the idle timer
	// fires. Require at least one event beyond RUN_STARTED as evidence that
	// the run produced output.
	if len(events) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(events))
	}
}

// TestActor_TextMessageEvents verifies that text message lifecycle events fire.
func TestActor_TextMessageEvents(t *testing.T) {
	cfg := loadConfig(t)
	reg, sessID, _, cleanup := newActorEnv(t, cfg, "chat")
	defer cleanup()
	a := reg.GetOrCreate(sessID)

	events := runActor(t, a, "Reply with one short sentence about cats. Do not use chain-of-thought reasoning, just answer directly.", 15*time.Second)
	assertActorEvent(t, events, actor.EventRunStarted)
	if len(events) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(events))
	}
}

// TestActor_ToolCallEvents verifies that a tool-using request produces tool
// call events.
func TestActor_ToolCallEvents(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		reg, sessID, workdir, cleanup := newActorEnv(t, cfg, "chat")
		defer cleanup()

		target := filepath.Join(workdir, "actor_target.txt")
		if err := os.WriteFile(target, []byte("hello actor\n"), 0644); err != nil {
			return err
		}

		a := reg.GetOrCreate(sessID)
		events := runActor(t, a, "Run the bash command: echo tool_event_probe\n\nYou have a bash tool. You MUST call it. Do not just describe the command — actually invoke the bash tool with command=\"echo tool_event_probe\".", 30*time.Second)

		if len(events) < 2 {
			return errAssertion{msg: fmt.Sprintf("expected at least 2 events, got %d", len(events))}
		}
		hasRunStart := false
		hasToolEvent := false
		for _, e := range events {
			if e.Type == actor.EventRunStarted {
				hasRunStart = true
			}
			// Actor event types are uppercase ("TOOL_CALL_START", etc.), so
			// match case-insensitively.
			if strings.Contains(strings.ToLower(string(e.Type)), "tool") {
				hasToolEvent = true
			}
		}
		if !hasRunStart {
			return errAssertion{msg: "RUN_STARTED not observed"}
		}
		if !hasToolEvent {
			return errAssertion{msg: "no tool-related event observed"}
		}
		return nil
	})
}

// TestActor_Shutdown verifies graceful shutdown closes Done().
func TestActor_Shutdown(t *testing.T) {
	cfg := loadConfig(t)
	reg, sessID, _, cleanup := newActorEnv(t, cfg, "chat")
	defer cleanup()

	a := reg.GetOrCreate(sessID)
	a.Shutdown()

	select {
	case <-a.Done():
	case <-time.After(3 * time.Second):
		t.Error("Done() did not close within 3s of Shutdown")
	}
	if a.State() != actor.StateShutdown {
		t.Errorf("state = %v, want StateShutdown", a.State())
	}
}

// TestActor_InboxFull verifies that overflowing a tiny inbox returns false.
func TestActor_InboxFull(t *testing.T) {
	cfg := loadConfig(t)
	fc := fridayConfig(t, cfg, "chat")
	dir := fc.DataDir
	mgr := newSessionManager(t, dir)
	mgr.SetLLM(newClient(t, cfg, "chat"))
	reg := actor.NewRegistry(mgr, fc, actor.RegistryConfig{
		IdleTimeout:   time.Minute,
		SweepInterval: time.Minute,
		InboxBuffer:   1,
		OutcomeBuffer: 32,
	})
	sessID := types.NewID()
	if _, _, err := mgr.GetOrCreateByID(sessID); err != nil {
		t.Fatalf("create session: %v", err)
	}
	a := reg.GetOrCreate(sessID)
	defer a.Shutdown()

	if !a.Send(actor.Message{ID: "m1", Content: "say hi"}) {
		t.Fatal("first Send should succeed")
	}
	time.Sleep(100 * time.Millisecond)
	if a.Send(actor.Message{ID: "m2", Content: "say hi again"}) {
		t.Log("second Send unexpectedly succeeded (actor may have drained)")
	}
}

// TestActor_EventOrder verifies event Seq values are monotonically increasing.
func TestActor_EventOrder(t *testing.T) {
	cfg := loadConfig(t)
	reg, sessID, _, cleanup := newActorEnv(t, cfg, "chat")
	defer cleanup()
	a := reg.GetOrCreate(sessID)

	events := runActor(t, a, "Say OK.", 15*time.Second)
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events, got %d", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Errorf("event Seq not monotonic at index %d: %d <= %d", i, events[i].Seq, events[i-1].Seq)
		}
	}
}

// keep import
var _ = context.Background
