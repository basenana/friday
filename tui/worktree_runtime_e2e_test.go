//go:build e2e

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/core/actor/events"
	coresession "github.com/basenana/friday/core/session"
)

func TestDefaultWorktreeRuntimeLifecycle(t *testing.T) {
	runProjectWorktreeRuntimeLifecycleE2E(t)
}

func runProjectWorktreeRuntimeLifecycleE2E(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	fixture := newWorktreeRuntimeTestFixture(t)
	runtimeA, err := activateDefaultWorktree(ctx, fixture.supervisor, fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	inputsA := installLifecycleE2ELoop(t, runtimeA)
	if err := runtimeA.loop.Start(ctx, runtimeA.lifecycle.Current(), "continue lifecycle A"); err != nil {
		t.Fatal(err)
	}
	assertLifecycleE2EPrompt(t, inputsA, runtimeA.sessionID, coderloop.BootstrapPrompt)

	m := newWorktreeRuntimeTestModel(t, fixture, runtimeA)
	registryA, loopA, feedA := runtimeA.registry, runtimeA.loop, m.feed
	m = submitLifecycleE2ENavigation(t, m, "/worktree lifecycle B")
	runtimeB := m.worktreeRuntime
	if runtimeB == nil || runtimeB == runtimeA {
		t.Fatal("busy /worktree did not create and activate worktree B")
	}
	if runtimeB.sessionID == runtimeA.sessionID {
		t.Fatal("worktrees A and B share one session")
	}
	assertLifecycleE2EFeedClosed(t, feedA)
	if m.feed == nil || m.feed == feedA {
		t.Fatal("/worktree did not install B's foreground feed")
	}
	if runtimeA.registry != registryA || runtimeA.loop != loopA {
		t.Fatal("/worktree replaced worktree A's retained runtime resources")
	}
	assertLifecycleE2ELoopState(t, runtimeA, coderloop.StateActive)
	if _, release, err := registryA.AcquireLifecycle(runtimeA.sessionID); err != nil {
		t.Fatalf("/worktree shut down worktree A's registry: %v", err)
	} else {
		release()
	}

	inputsB := installLifecycleE2ELoop(t, runtimeB)
	m.loopManager = runtimeB.loop
	if err := runtimeB.loop.Start(ctx, runtimeB.lifecycle.Current(), "continue lifecycle B"); err != nil {
		t.Fatal(err)
	}
	assertLifecycleE2EPrompt(t, inputsB, runtimeB.sessionID, coderloop.BootstrapPrompt)
	feedB := m.feed
	m = submitLifecycleE2ENavigation(t, m, "/select "+runtimeA.id)
	if m.worktreeRuntime != runtimeA || m.registry != registryA || m.loopManager != loopA {
		t.Fatal("busy /select did not reactivate retained worktree A")
	}
	assertLifecycleE2EFeedClosed(t, feedB)
	assertLifecycleE2ELoopState(t, runtimeA, coderloop.StateActive)
	assertLifecycleE2ELoopState(t, runtimeB, coderloop.StateActive)
	feedA = m.feed
	m = submitLifecycleE2ENavigation(t, m, "/select "+runtimeB.id)
	if m.worktreeRuntime != runtimeB || m.loopManager != runtimeB.loop {
		t.Fatal("second /select did not return to retained worktree B")
	}
	assertLifecycleE2EFeedClosed(t, feedA)

	sessionA, sessionB := runtimeA.sessionID, runtimeB.sessionID
	m.closeFeed()
	if err := fixture.supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	assertLifecycleE2ELoopState(t, runtimeA, coderloop.StateSuspended)
	assertLifecycleE2ELoopState(t, runtimeB, coderloop.StateSuspended)

	restarted, err := newWorktreeRuntimeSupervisor(fixture.service, fixture.sessions, fixture.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted supervisor: %v", err)
		}
	})
	recoveryInputs := make(chan bus.Envelope, 4)
	restarted.attachRuntime = func(ctx context.Context, runtime *worktreeRuntime) error {
		runtime.loop.Close()
		runtime.loop = coderloop.NewManager(eventbus.NewBus(), coderloop.WithInputDispatcher(func(env bus.Envelope) error {
			recoveryInputs <- env
			return nil
		}))
		t.Cleanup(runtime.loop.Close)
		return restarted.defaultAttachRuntime(ctx, runtime)
	}
	if err := restarted.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	wantRecovery := map[string]bool{sessionA: true, sessionB: true}
	for range 2 {
		env := waitLifecycleE2EInput(t, recoveryInputs)
		if !wantRecovery[env.Session] {
			t.Fatalf("recovery input for unexpected session %q", env.Session)
		}
		delete(wantRecovery, env.Session)
		assertLifecycleE2EEnvelopePrompt(t, env, coderloop.DevelopRecoveryPrompt)
	}
	if len(wantRecovery) != 0 {
		t.Fatalf("missing loop recovery inputs for sessions %#v", wantRecovery)
	}
	for _, original := range []*worktreeRuntime{runtimeA, runtimeB} {
		recovered := restarted.runtimes[original.id]
		if recovered == nil || recovered.sessionID != original.sessionID {
			t.Fatalf("restored runtime %q = %#v, want session %q", original.id, recovered, original.sessionID)
		}
		assertLifecycleE2ELoopState(t, recovered, coderloop.StateActive)
	}

	fixture.supervisor = restarted
	recoveredA := restarted.runtimes[runtimeA.id]
	recoveredB := restarted.runtimes[runtimeB.id]
	if _, err := restarted.Activate(ctx, recoveredA.id); err != nil {
		t.Fatal(err)
	}
	m = newWorktreeRuntimeTestModel(t, fixture, recoveredA)
	recoveredB.loop.Close()
	assertLifecycleE2ELoopState(t, recoveredB, coderloop.StateSuspended)
	if err := fixture.sessions.DeleteRoot(sessionB); err != nil {
		t.Fatal(err)
	}
	feedA = m.feed
	m = submitLifecycleE2ENavigation(t, m, "/select "+recoveredB.id)
	replacementB := m.worktreeRuntime
	if replacementB == recoveredB {
		t.Fatal("selecting B reused the cached runtime with a missing session")
	}
	if replacementB.sessionID == sessionB {
		t.Fatal("selecting B did not install a replacement session")
	}
	assertLifecycleE2EFeedClosed(t, feedA)
	metaB, err := restarted.store.Get(recoveredB.id)
	if err != nil {
		t.Fatal(err)
	}
	if metaB.SessionID != replacementB.sessionID {
		t.Fatalf("B metadata session = %q, want replacement %q", metaB.SessionID, replacementB.sessionID)
	}
	if _, err := replacementB.lifecycle.Current().ReadRecord(ctx, coderloop.StateNamespace); !errors.Is(err, coresession.ErrRecordNotFound) {
		t.Fatalf("replacement B inherited deleted loop state: %v", err)
	}
}

func installLifecycleE2ELoop(t *testing.T, runtime *worktreeRuntime) chan bus.Envelope {
	t.Helper()
	runtime.loop.Close()
	inputs := make(chan bus.Envelope, 2)
	runtime.loop = coderloop.NewManager(eventbus.NewBus(), coderloop.WithInputDispatcher(func(env bus.Envelope) error {
		inputs <- env
		return nil
	}))
	t.Cleanup(runtime.loop.Close)
	return inputs
}

func submitLifecycleE2ENavigation(t *testing.T, m *model, command string) *model {
	t.Helper()
	m.running = true
	m.textarea.SetValue(command)
	updated, cmd := m.submitComposer()
	m = updated.(*model)
	if cmd == nil {
		t.Fatalf("busy navigation %q was not dispatched immediately", command)
	}
	if len(m.queued) != 0 {
		t.Fatalf("busy navigation %q entered the queue: %#v", command, m.queued)
	}
	prepared := worktreePreparedFromCommand(t, cmd)
	if prepared.err != nil {
		t.Fatalf("prepare navigation %q: %v", command, prepared.err)
	}
	updated, _ = m.Update(prepared)
	m = updated.(*model)
	if len(m.queued) != 0 {
		t.Fatalf("navigation %q retained queued input: %#v", command, m.queued)
	}
	return m
}

func assertLifecycleE2EFeedClosed(t *testing.T, feed *bus.Feed) {
	t.Helper()
	select {
	case <-feed.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("previous foreground feed remained subscribed")
	}
}

func assertLifecycleE2EPrompt(t *testing.T, inputs <-chan bus.Envelope, sessionID, want string) {
	t.Helper()
	env := waitLifecycleE2EInput(t, inputs)
	if env.Session != sessionID {
		t.Fatalf("loop input session = %q, want %q", env.Session, sessionID)
	}
	assertLifecycleE2EEnvelopePrompt(t, env, want)
}

func waitLifecycleE2EInput(t *testing.T, inputs <-chan bus.Envelope) bus.Envelope {
	t.Helper()
	select {
	case env := <-inputs:
		return env
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for loop input")
		return bus.Envelope{}
	}
}

func assertLifecycleE2EEnvelopePrompt(t *testing.T, env bus.Envelope, want string) {
	t.Helper()
	var input bus.AgentTextInput
	if err := events.DecodePayload(env.Event, &input); err != nil {
		t.Fatal(err)
	}
	if input.Text != want {
		t.Fatalf("loop prompt = %q, want %q", input.Text, want)
	}
}

func assertLifecycleE2ELoopState(t *testing.T, runtime *worktreeRuntime, want coderloop.State) {
	t.Helper()
	raw, err := runtime.lifecycle.Current().ReadRecord(context.Background(), coderloop.StateNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if got := coderloop.State(strings.TrimSpace(string(raw))); got != want {
		t.Fatalf("runtime %q loop state = %q, want %q", runtime.id, got, want)
	}
}
