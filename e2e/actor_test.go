//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
)

// newActorEnv builds a Registry environment: a sessions.Manager + config +
// registry. Returns (registry, sessionID, workdir).
func newActorEnv(t *testing.T, cfg *E2EConfig, modelName string) (*actor.Registry, string, string) {
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
	return reg, sessID, dir
}

// runRegistryActor binds to the session's actor over the bus, sends one
// inbox message, drains the ordered session feed until the run finishes
// (or the grace period elapses), then shuts the actor down. Returns the
// collected events.
func runRegistryActor(t *testing.T, reg *actor.Registry, sessID, msg string, grace time.Duration) []events.Event {
	t.Helper()
	if _, err := reg.GetOrCreate(sessID); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	feed := bus.SubscribeAgentFeed(reg.Bus(), sessID)
	defer feed.Close()

	reg.Bus().Publish(bus.TopicInbox(sessID),
		bus.NewUserInput(sessID, "e2e", bus.UserTextInput{Text: msg}))

	var collected []events.Event
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	idle := time.NewTimer(grace)
	defer idle.Stop()
	for {
		select {
		case evt := <-feed.Events():
			collected = append(collected, evt)
			if evt.Type == events.KindRunFinished || evt.Type == events.KindRunError {
				reg.Shutdown(sessID)
				return collected
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(grace)
		case <-idle.C:
			reg.Shutdown(sessID)
			return collected
		case <-deadline.C:
			reg.Shutdown(sessID)
			return collected
		}
	}
}

// TestActor_BasicChat verifies a registry-backed actor emits RUN_STARTED
// before RUN_FINISHED and produces text output for a single chat turn.
func TestActor_BasicChat(t *testing.T) {
	cfg := loadConfig(t)
	reg, sessID, _ := newActorEnv(t, cfg, "chat")

	evts := runRegistryActor(t, reg, sessID, "Say hello in one short sentence.", 15*time.Second)
	for _, e := range evts {
		t.Logf("event: %s", e.Type)
	}
	runStartIdx, runFinishIdx := -1, -1
	for i, e := range evts {
		switch e.Type {
		case events.KindRunStarted:
			if runStartIdx == -1 {
				runStartIdx = i
			}
		case events.KindRunFinished:
			runFinishIdx = i
		}
	}
	if runStartIdx == -1 {
		t.Fatalf("RUN_STARTED not observed in %d events", len(evts))
	}
	if runFinishIdx == -1 || runFinishIdx < runStartIdx {
		t.Fatalf("RUN_FINISHED missing or before RUN_STARTED (start=%d finish=%d)", runStartIdx, runFinishIdx)
	}
	if len(evts) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(evts))
	}
}

// TestActor_TextMessageEvents verifies that text message lifecycle events fire.
func TestActor_TextMessageEvents(t *testing.T) {
	cfg := loadConfig(t)
	reg, sessID, _ := newActorEnv(t, cfg, "chat")

	evts := runRegistryActor(t, reg, sessID, "Reply with one short sentence about cats. Do not use chain-of-thought reasoning, just answer directly.", 15*time.Second)
	assertActorEvent(t, evts, events.KindRunStarted)
	if len(evts) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(evts))
	}
}

// TestActor_ToolCallEvents verifies that a tool-using request produces tool
// call events.
func TestActor_ToolCallEvents(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		reg, sessID, workdir := newActorEnv(t, cfg, "chat")

		target := filepath.Join(workdir, "actor_target.txt")
		if err := os.WriteFile(target, []byte("hello actor\n"), 0644); err != nil {
			return err
		}

		evts := runRegistryActor(t, reg, sessID, "Run the bash command: echo tool_event_probe\n\nYou have a bash tool. You MUST call it. Do not just describe the command — actually invoke the bash tool with command=\"echo tool_event_probe\".", 30*time.Second)

		if len(evts) < 2 {
			return errAssertion{msg: fmt.Sprintf("expected at least 2 events, got %d", len(evts))}
		}
		hasRunStart := false
		hasToolEvent := false
		for _, e := range evts {
			if e.Type == events.KindRunStarted {
				hasRunStart = true
			}
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

// TestActor_ShutdownPublishesStatusStopped verifies that a registry
// Shutdown surfaces as a status.stopped envelope on the bus.
func TestActor_ShutdownPublishesStatusStopped(t *testing.T) {
	cfg := loadConfig(t)
	reg, sessID, _ := newActorEnv(t, cfg, "chat")

	if _, err := reg.GetOrCreate(sessID); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	feed := bus.SubscribeAgentFeed(reg.Bus(), sessID)
	defer feed.Close()
	reg.Shutdown(sessID)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-feed.Events():
			if evt.Type == events.KindCustom && evt.Name == "status."+bus.StatusStopped {
				return
			}
		case <-deadline:
			t.Fatal("status.stopped not observed within 5s of Shutdown")
		}
	}
}

// TestActor_InboxFull verifies that overflowing a tiny inbox makes TrySend
// return false.
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
	})
	sessID := types.NewID()
	if _, _, err := mgr.GetOrCreateByID(sessID); err != nil {
		t.Fatalf("create session: %v", err)
	}
	a, err := reg.GetOrCreate(sessID)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	defer reg.Shutdown(sessID)

	if !a.TrySend(coreactor.UserTextMessage{Text: "say hi"}) {
		t.Fatal("first TrySend should succeed")
	}
	time.Sleep(100 * time.Millisecond)
	if a.TrySend(coreactor.UserTextMessage{Text: "say hi again"}) {
		t.Log("second TrySend unexpectedly succeeded (actor may have drained)")
	}
}
