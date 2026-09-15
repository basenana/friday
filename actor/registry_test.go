package actor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/config"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/setup"
)

func TestCountRunningTasks(t *testing.T) {
	tasks := []*sandbox.Task{nil, {Status: sandbox.TaskRunning}, {Status: sandbox.TaskCompleted}, {Status: sandbox.TaskRunning}}
	if got := countRunningTasks(tasks); got != 2 {
		t.Fatalf("countRunningTasks = %d, want 2", got)
	}
}

func newTestRegistry(t *testing.T, cfgMod func(*RegistryConfig)) *Registry {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Workspace = filepath.Join(cfg.DataDir, "workspace")
	cfg.Memory.Enabled = false
	cfg.Sandbox.Sandbox.Enabled = false

	store := file.NewFileSessionStore(cfg.SessionsPath())
	sessMgr := sessions.NewManager(store, filepath.Join(cfg.DataDir, "current"), "")

	rcfg := DefaultRegistryConfig()
	rcfg.ShutdownGrace = 500 * time.Millisecond
	if cfgMod != nil {
		cfgMod(&rcfg)
	}
	return NewRegistry(sessMgr, cfg, rcfg)
}

func TestRegistry_GetOrCreateIdempotent(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()

	a1, err := r.GetOrCreate("sess-1")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	a2, err := r.GetOrCreate("sess-1")
	if err != nil {
		t.Fatalf("GetOrCreate second: %v", err)
	}
	if a1 != a2 {
		t.Fatalf("expected the same actor instance")
	}
	if got, ok := r.Get("sess-1"); !ok || got != a1 {
		t.Fatalf("Get mismatch")
	}
}

func TestRegistryPersistsActorEventsWhenStoreSupportsIt(t *testing.T) {
	r := newTestRegistry(t, nil)
	a, err := r.GetOrCreate("sess-events")
	if err != nil {
		t.Fatal(err)
	}
	a.EmitCustom(events.CustomTodoUpdate, "todo", map[string]any{"status": "running"})
	r.Shutdown("sess-events")
	mgr := r.sessMgr.(*sessions.Manager)
	store := mgr.GetStore().(sessions.EventStore)
	got, err := store.LoadEvents(context.Background(), "sess-events")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != events.CustomTodoUpdate {
		t.Fatalf("persisted events = %#v", got)
	}
	r.ShutdownAll()
}

func TestRegistry_ShutdownThenRebuild(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()

	a1, err := r.GetOrCreate("sess-1")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	r.Shutdown("sess-1")
	if _, ok := r.Get("sess-1"); ok {
		t.Fatalf("expected actor to be gone after Shutdown")
	}
	a2, err := r.GetOrCreate("sess-1")
	if err != nil {
		t.Fatalf("GetOrCreate after Shutdown: %v", err)
	}
	if a2 == a1 {
		t.Fatalf("expected a fresh actor after Shutdown")
	}
}

func TestRegistry_SubscribeMissingSession(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()

	if _, err := r.Subscribe("nope"); err == nil {
		t.Fatalf("expected error subscribing to unknown session")
	}
}

func TestRegistry_ShutdownClosesSubscription(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()

	a, err := r.GetOrCreate("sess-1")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	sub := a.Subscribe()
	r.Shutdown("sess-1")

	select {
	case _, ok := <-sub.Events():
		if ok {
			t.Fatalf("expected closed channel, got an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("subscription channel not closed after Shutdown")
	}
}

func TestRegistryDetachStaleKeepsReplacementIsolated(t *testing.T) {
	old := &managedActor{}
	old.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	r := &Registry{
		entries: map[string]*managedActor{"sess-1": old},
		cfg:     RegistryConfig{IdleTimeout: time.Minute},
	}

	stale := r.detachStale(time.Now())
	if len(stale) != 1 || stale[0].managed != old {
		t.Fatalf("detached entries = %+v, want the original actor", stale)
	}
	replacement := &managedActor{}
	r.entries["sess-1"] = replacement
	if got := r.entries["sess-1"]; got != replacement {
		t.Fatal("detached stale actor still aliases the replacement entry")
	}
}

func TestRegistry_IdleSweepEvicts(t *testing.T) {
	r := newTestRegistry(t, func(c *RegistryConfig) {
		c.IdleTimeout = 50 * time.Millisecond
		c.SweepInterval = 20 * time.Millisecond
	})
	defer r.ShutdownAll()

	if _, err := r.GetOrCreate("sess-1"); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		if _, ok := r.Get("sess-1"); !ok {
			return // evicted
		}
		select {
		case <-deadline:
			t.Fatalf("idle actor was not evicted by the sweep loop")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRegistryGetOrCreateRefreshesIdleDeadline(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()
	if _, err := r.GetOrCreate("sess-touch"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	entry := r.entries["sess-touch"]
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	r.mu.Unlock()

	if _, err := r.GetOrCreate("sess-touch"); err != nil {
		t.Fatal(err)
	}
	if stale := r.detachStale(time.Now()); len(stale) != 0 {
		t.Fatalf("cached GetOrCreate did not refresh activity: %+v", stale)
	}
}

func TestRegistryLifecycleLeasePreventsIdleEviction(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()
	_, release, err := r.AcquireLifecycle("sess-lease")
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	entry := r.entries["sess-lease"]
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	r.mu.Unlock()
	if stale := r.detachStale(time.Now()); len(stale) != 0 {
		t.Fatalf("leased actor was detached: %+v", stale)
	}

	release()
	release() // release is intentionally idempotent
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	stale := r.detachStale(time.Now())
	if len(stale) != 1 || stale[0].managed != entry {
		t.Fatalf("released actor was not eligible for eviction: %+v", stale)
	}
	r.stopEntry(stale[0], true)
}

func TestRegistryActiveTurnPreventsIdleEviction(t *testing.T) {
	entry := &managedActor{}
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	if err := entry.OnTurnStart(context.Background(), coreactor.TurnStartInfo{}); err != nil {
		t.Fatal(err)
	}
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	r := &Registry{
		entries: map[string]*managedActor{"sess-turn": entry},
		cfg:     RegistryConfig{IdleTimeout: time.Minute},
	}
	if stale := r.detachStale(time.Now()); len(stale) != 0 {
		t.Fatalf("active turn was detached: %+v", stale)
	}
	if err := entry.OnTurnComplete(context.Background(), "turn", coreactor.TurnOutcome{}); err != nil {
		t.Fatal(err)
	}
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	if stale := r.detachStale(time.Now()); len(stale) != 1 {
		t.Fatalf("completed turn did not become evictable: %+v", stale)
	}
}

func TestRegistryRunningBackgroundTaskPreventsIdleEviction(t *testing.T) {
	sandboxCfg := sandbox.DefaultConfig()
	sandboxCfg.Sandbox.Enabled = false
	sandboxCfg.Permissions.Allow = append(sandboxCfg.Permissions.Allow, "sleep")
	tasks := sandbox.NewTaskManager(sandbox.NewExecutor(sandboxCfg))
	task, err := tasks.Start("sleep 10", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.KillAll()
	entry := &managedActor{agentCtx: &setup.AgentContext{TaskManager: tasks}}
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	r := &Registry{
		entries: map[string]*managedActor{"sess-task": entry},
		cfg:     RegistryConfig{IdleTimeout: time.Minute},
	}
	if stale := r.detachStale(time.Now()); len(stale) != 0 {
		t.Fatalf("actor with running task was detached: %+v", stale)
	}
	if err := tasks.Kill(task.ID); err != nil {
		t.Fatal(err)
	}
	entry.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	if stale := r.detachStale(time.Now()); len(stale) != 1 {
		t.Fatalf("actor with terminal task did not become evictable: %+v", stale)
	}
}

func TestRegistryDispatchInputRecreatesEvictedActor(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()
	old, err := r.GetOrCreate("sess-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	r.Shutdown("sess-dispatch")
	if err := r.DispatchInput(bus.NewUserInput("sess-dispatch", "test", bus.UserTextInput{
		Text: "hello", Delivery: bus.InputDelivery("invalid-test-delivery"),
	})); err != nil {
		t.Fatal(err)
	}
	rebuilt, ok := r.Get("sess-dispatch")
	if !ok || rebuilt == old {
		t.Fatalf("user input did not rebuild actor: old=%p rebuilt=%p live=%v", old, rebuilt, ok)
	}
}

func TestRegistryStatefulInputDoesNotCreateActor(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()
	err := r.DispatchInput(bus.NewFormSubmit("sess-missing", "test", bus.FormSubmitInput{
		FormID: "form-1", Values: map[string]any{"answer": "yes"},
	}))
	if err == nil {
		t.Fatal("stateful form input unexpectedly succeeded without a live actor")
	}
	if _, ok := r.Get("sess-missing"); ok {
		t.Fatal("stateful form input created a replacement actor")
	}
}

func TestRegistryRestoresCompletedBackgroundTasksAfterRebuild(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()
	const sessionID = "sess-persisted-task"
	if _, err := r.GetOrCreate(sessionID); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	tasks := r.entries[sessionID].agentCtx.TaskManager
	r.mu.Unlock()
	task, err := tasks.Start("echo registry-persisted", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	completed, err := tasks.Wait(task.ID, 5*time.Second)
	if err != nil || completed.Status != sandbox.TaskCompleted {
		t.Fatalf("completed task = %+v, err = %v", completed, err)
	}

	r.Shutdown(sessionID)
	if _, err := r.GetOrCreate(sessionID); err != nil {
		t.Fatal(err)
	}
	restored := r.ListTasks(sessionID)
	if len(restored) != 1 || restored[0].ID != task.ID || !strings.Contains(restored[0].Output, "registry-persisted") {
		t.Fatalf("restored tasks = %+v", restored)
	}
}

func TestRegistry_ShutdownAll(t *testing.T) {
	r := newTestRegistry(t, nil)

	for _, id := range []string{"a", "b"} {
		if _, err := r.GetOrCreate(id); err != nil {
			t.Fatalf("GetOrCreate(%s): %v", id, err)
		}
	}
	r.ShutdownAll()
	for _, id := range []string{"a", "b"} {
		if _, ok := r.Get(id); ok {
			t.Fatalf("actor %s survived ShutdownAll", id)
		}
	}
	// Idempotent.
	r.ShutdownAll()
}

func TestResolveTaskIDSupportsUniquePrefixes(t *testing.T) {
	tasks := []*sandbox.Task{{ID: "abcdef1234"}, {ID: "abc9999999"}}
	if got, err := resolveTaskID(tasks, "abcdef"); err != nil || got != "abcdef1234" {
		t.Fatalf("unique prefix = %q, err=%v", got, err)
	}
	if _, err := resolveTaskID(tasks, "abc"); err == nil {
		t.Fatal("ambiguous prefix was accepted")
	}
	if _, err := resolveTaskID(tasks, "missing"); err == nil {
		t.Fatal("missing task was accepted")
	}
}

func TestRegistry_SendTrySendPreempt(t *testing.T) {
	r := newTestRegistry(t, nil)
	defer r.ShutdownAll()

	a, err := r.GetOrCreate("sess-1")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if !a.TrySend(coreactor.UserTextMessage{Text: "hello"}) {
		t.Fatalf("TrySend on a fresh actor should succeed")
	}
	if err := a.SendPreempt(context.Background(), "test"); err != nil {
		t.Fatalf("SendPreempt: %v", err)
	}
	// Give the loop a moment to drain; a second message must still be
	// accepted (actor stays alive after preempt).
	time.Sleep(50 * time.Millisecond)
	if !a.TrySend(coreactor.UserTextMessage{Text: "again"}) {
		t.Fatalf("TrySend after preempt should succeed")
	}
}

func TestNewFilePathValidator(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "notes.txt")
	if err := writeFile(inside, "x"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	v := newFilePathValidator(dir)

	if err := v(inside); err != nil {
		t.Fatalf("absolute inside path: %v", err)
	}
	if err := v("notes.txt"); err != nil {
		t.Fatalf("workdir-relative path: %v", err)
	}
	if err := v("../escape.txt"); err == nil {
		t.Fatalf("expected error for path escaping workdir")
	}
	if err := v("/etc/passwd"); err == nil {
		t.Fatalf("expected error for absolute path outside workdir")
	}
	if err := v(filepath.Join(dir, "missing.txt")); err == nil {
		t.Fatalf("expected error for non-existent path")
	}
	if err := v("  "); err == nil {
		t.Fatalf("expected error for blank path")
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
