package sessions_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

func TestLifecycleChildrenAndClose(t *testing.T) {
	dir := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(dir, "sessions"))
	manager := sessions.NewManager(store, filepath.Join(dir, "current"), "")
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := lifecycle.Fork()
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := lifecycle.CreateTemporary()
	if err != nil {
		t.Fatal(err)
	}
	if fork.Root != lifecycle.Current() || fork.Parent != lifecycle.Current() {
		t.Fatal("fork is not attached to lifecycle root")
	}
	if temporary.Root != lifecycle.Current() || temporary.Parent != lifecycle.Current() || !temporary.Temporary {
		t.Fatal("temporary session is not correctly attached")
	}
	if err := lifecycle.Release(fork); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Release(coresession.New("foreign", nil)); err == nil {
		t.Fatal("foreign child release unexpectedly succeeded")
	}
	if err := lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.Fork(); !errors.Is(err, sessions.ErrLifecycleClosed) {
		t.Fatalf("fork after close = %v", err)
	}
}

func TestAssociatedSessionConcurrentCreationIsUnique(t *testing.T) {
	dir := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(dir, "sessions"))
	manager := sessions.NewManager(store, filepath.Join(dir, "current"), "")
	first, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID := first.RootID()
	second, err := manager.OpenRoot(context.Background(), rootID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()

	ids := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, lifecycle := range []sessions.SessionLifecycle{first, second} {
		wg.Add(1)
		go func(lifecycle sessions.SessionLifecycle) {
			defer wg.Done()
			child, _, err := lifecycle.GetOrCreateAssociated(context.Background(), sessions.AssociatedSpec{Key: "proposal/shared/self"})
			if err != nil {
				errs <- err
				return
			}
			ids <- child.ID
		}(lifecycle)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var firstID string
	for id := range ids {
		if firstID == "" {
			firstID = id
		} else if id != firstID {
			t.Fatalf("concurrent associated IDs differ: %s != %s", id, firstID)
		}
	}
}

func TestAssociatedSessionResumesByRootAndKey(t *testing.T) {
	dir := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(dir, "sessions"))
	manager := sessions.NewManager(store, filepath.Join(dir, "current"), "")
	first, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID := first.RootID()
	child, created, err := first.GetOrCreateAssociated(context.Background(), sessions.AssociatedSpec{Key: "proposal/p1/self"})
	if err != nil || !created {
		t.Fatalf("first associated created=%v err=%v", created, err)
	}
	_ = first.Close()

	second, err := manager.OpenRoot(context.Background(), rootID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resumed, created, err := second.GetOrCreateAssociated(context.Background(), sessions.AssociatedSpec{Key: "proposal/p1/self"})
	if err != nil || created || resumed.ID != child.ID {
		t.Fatalf("resumed=%v created=%v err=%v", resumed, created, err)
	}
	_ = second.Close()
}
