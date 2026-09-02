package actor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basenana/friday/config"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
)

func newTestRegistry(t *testing.T, cfgMod func(*RegistryConfig)) *Registry {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Workspace = filepath.Join(cfg.DataDir, "workspace")
	cfg.Memory.Enabled = false

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
