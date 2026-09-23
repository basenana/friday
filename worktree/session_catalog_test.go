package worktree

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
)

func TestSessionCatalogReusesReferencedActiveRoot(t *testing.T) {
	catalog, store, manager, worktreeID := newSessionCatalog(t)
	referenced := createCatalogRoot(t, manager)
	if err := store.UpdateSession(worktreeID, referenced); err != nil {
		t.Fatal(err)
	}

	lifecycle, id, created, err := catalog.EnsureRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lifecycle.Close()
	if id != referenced || lifecycle.RootID() != referenced || created {
		t.Fatalf("EnsureRoot = root %q, lifecycle %q, created %t; want referenced root reused", id, lifecycle.RootID(), created)
	}
}

func TestSessionCatalogReplacesMissingReferencedRoot(t *testing.T) {
	catalog, store, manager, worktreeID := newSessionCatalog(t)
	if err := store.UpdateSession(worktreeID, "missing-session"); err != nil {
		t.Fatal(err)
	}

	lifecycle, id, created, err := catalog.EnsureRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lifecycle.Close()
	if !created || id == "missing-session" || lifecycle.RootID() != id {
		t.Fatalf("EnsureRoot = root %q, lifecycle %q, created %t; want replacement", id, lifecycle.RootID(), created)
	}
	assertCatalogReference(t, store, worktreeID, id)
	if active, err := manager.IsActive(id); err != nil || !active {
		t.Fatalf("replacement session active = %t, err = %v; want true, nil", active, err)
	}
}

func TestSessionCatalogReplacesArchivedReferencedRoot(t *testing.T) {
	catalog, store, manager, worktreeID := newSessionCatalog(t)
	archived := createCatalogRoot(t, manager)
	if err := manager.Archive(archived); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSession(worktreeID, archived); err != nil {
		t.Fatal(err)
	}

	lifecycle, id, created, err := catalog.EnsureRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lifecycle.Close()
	if !created || id == archived || lifecycle.RootID() != id {
		t.Fatalf("EnsureRoot = root %q, lifecycle %q, created %t; want archived root replaced", id, lifecycle.RootID(), created)
	}
	assertCatalogReference(t, store, worktreeID, id)
}

func TestSessionCatalogConcurrentRepairCreatesOneReplacement(t *testing.T) {
	catalog, store, manager, worktreeID := newSessionCatalog(t)
	if err := store.UpdateSession(worktreeID, "missing-session"); err != nil {
		t.Fatal(err)
	}

	const callers = 16
	type result struct {
		id      string
		created bool
		err     error
	}
	results := make(chan result, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			lifecycle, id, created, err := catalog.EnsureRoot(context.Background(), nil)
			if lifecycle != nil {
				defer lifecycle.Close()
			}
			results <- result{id: id, created: created, err: err}
		}()
	}
	group.Wait()
	close(results)

	var replacement string
	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if replacement == "" {
			replacement = result.id
		}
		if result.id != replacement {
			t.Fatalf("concurrent repair returned %q and %q; want one replacement", replacement, result.id)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created %d replacements; want exactly one", createdCount)
	}
	assertCatalogReference(t, store, worktreeID, replacement)
	metas, err := manager.GetStore().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].ID != replacement {
		t.Fatalf("persisted session metadata = %#v; want only replacement %q", metas, replacement)
	}
}

func TestSessionCatalogOpenRootRejectsOtherWorktreeRoot(t *testing.T) {
	catalog, store, manager, worktreeID := newSessionCatalog(t)
	referenced := createCatalogRoot(t, manager)
	other := createCatalogRoot(t, manager)
	if err := store.UpdateSession(worktreeID, referenced); err != nil {
		t.Fatal(err)
	}

	if _, err := catalog.OpenRoot(context.Background(), other, nil); err == nil {
		t.Fatalf("OpenRoot(%q) succeeded; want rejection for a root owned by another worktree", other)
	}
	lifecycle, err := catalog.OpenRoot(context.Background(), referenced, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lifecycle.Close()
	if lifecycle.RootID() != referenced {
		t.Fatalf("OpenRoot root = %q, want %q", lifecycle.RootID(), referenced)
	}
}

func TestSessionCatalogOpenRootRepairsStaleReferenceBeforeRejectingIt(t *testing.T) {
	catalog, store, manager, worktreeID := newSessionCatalog(t)
	if err := store.UpdateSession(worktreeID, "missing-session"); err != nil {
		t.Fatal(err)
	}

	if _, err := catalog.OpenRoot(context.Background(), "missing-session", nil); err == nil {
		t.Fatal("OpenRoot accepted a stale root ID")
	}
	meta, err := store.Get(worktreeID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID == "" || meta.SessionID == "missing-session" {
		t.Fatalf("stale reference was not repaired: %q", meta.SessionID)
	}
	if active, err := manager.IsActive(meta.SessionID); err != nil || !active {
		t.Fatalf("replacement session active = %t, err = %v; want true, nil", active, err)
	}
}

func TestSessionCatalogCleansReplacementWhenMetadataCommitFails(t *testing.T) {
	_, store, manager, worktreeID := newSessionCatalog(t)
	failingStore := &commitFailStore{Store: store}
	catalog := NewSessionCatalog(failingStore, worktreeID, manager)

	if _, _, _, err := catalog.EnsureRoot(context.Background(), nil); !errors.Is(err, errMetadataCommit) {
		t.Fatalf("EnsureRoot error = %v, want metadata commit failure", err)
	}
	if !failingStore.callbackRan {
		t.Fatal("metadata callback did not run before commit failure")
	}
	metas, err := manager.GetStore().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("metadata commit failure orphaned sessions: %#v", metas)
	}
}

func TestSessionCatalogOpenRootRejectsBlankIDWithoutRepairing(t *testing.T) {
	for _, id := range []string{"", " \t\n"} {
		t.Run("id="+id, func(t *testing.T) {
			catalog, store, manager, worktreeID := newSessionCatalog(t)
			if err := store.UpdateSession(worktreeID, "missing-session"); err != nil {
				t.Fatal(err)
			}

			if _, err := catalog.OpenRoot(context.Background(), id, nil); err == nil {
				t.Fatalf("OpenRoot(%q) succeeded; want blank ID rejection", id)
			}
			assertCatalogReference(t, store, worktreeID, "missing-session")
			metas, err := manager.GetStore().List()
			if err != nil {
				t.Fatal(err)
			}
			if len(metas) != 0 {
				t.Fatalf("OpenRoot(%q) created sessions: %#v", id, metas)
			}
		})
	}
}

func newSessionCatalog(t *testing.T) (*SessionCatalog, Store, *sessions.Manager, string) {
	t.Helper()
	checkout := filepath.Join(t.TempDir(), "checkout")
	store, err := NewStore(filepath.Join(t.TempDir(), "projects"), "project-123")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.Ensure(Metadata{ID: "checkout-1", Name: "checkout", Path: checkout, Branch: "friday/checkout"})
	if err != nil {
		t.Fatal(err)
	}
	manager := sessions.NewManager(file.NewFileSessionStore(filepath.Join(t.TempDir(), "sessions")), filepath.Join(t.TempDir(), "current"), "")
	return NewSessionCatalog(store, meta.ID, manager), store, manager, meta.ID
}

func createCatalogRoot(t *testing.T, manager *sessions.Manager) string {
	t.Helper()
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertCatalogReference(t *testing.T, store Store, worktreeID, want string) {
	t.Helper()
	meta, err := store.Get(worktreeID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != want {
		t.Fatalf("worktree SessionID = %q, want %q", meta.SessionID, want)
	}
}

var errMetadataCommit = errors.New("metadata commit failed")

type commitFailStore struct {
	Store
	callbackRan bool
}

func (s *commitFailStore) UpdateMetadata(id string, update func(*Metadata) error) error {
	meta, err := s.Get(id)
	if err != nil {
		return err
	}
	if err := update(&meta); err != nil {
		return err
	}
	s.callbackRan = true
	return errMetadataCommit
}
