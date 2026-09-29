package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

type adoptFixture struct {
	store        Store
	pool         *project.Project
	sessions     *sessions.Manager
	projectRoot  string
	mainMeta     Metadata
	featureMeta  Metadata
	sessionStore *sessionfile.FileSessionStore
}

func newAdoptFixture(t *testing.T) *adoptFixture {
	t.Helper()
	data := t.TempDir()
	projectRoot := filepath.Join(data, "checkout")
	featureRoot := filepath.Join(data, "linked")
	for _, dir := range []string{projectRoot, featureRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewStore(filepath.Join(data, "projects"), "project-123")
	if err != nil {
		t.Fatal(err)
	}
	mainMeta, err := store.Ensure(Metadata{ID: "checkout-main", Name: "checkout", Path: projectRoot, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	featureMeta, err := store.Ensure(Metadata{ID: "checkout-feature", Name: "linked", Path: featureRoot, Branch: "friday/linked"})
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := sessionfile.NewFileSessionStore(filepath.Join(data, "sessions"))
	manager := sessions.NewManager(sessionStore, filepath.Join(data, "current"), "")
	pool, err := project.Open(projectRoot, project.NewFileStore(filepath.Join(data, "projects")))
	if err != nil {
		t.Fatal(err)
	}
	return &adoptFixture{
		store: store, pool: pool, sessions: manager, projectRoot: projectRoot,
		mainMeta: mainMeta, featureMeta: featureMeta, sessionStore: sessionStore,
	}
}

func (f *adoptFixture) createRoot(t *testing.T) string {
	t.Helper()
	lifecycle, err := f.sessions.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *adoptFixture) adopt(t *testing.T) {
	t.Helper()
	if err := AdoptSessions(context.Background(), f.store, f.projectRoot, f.pool, f.sessions); err != nil {
		t.Fatal(err)
	}
}

func (f *adoptFixture) assertNoLegacyReference(t *testing.T) {
	t.Helper()
	items, err := f.store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.SessionID != "" {
			t.Fatalf("legacy reference of %s survived adoption: %q", item.ID, item.SessionID)
		}
	}
}

func TestAdoptSessionsMovesMainAndLinkedReferencesIntoSeparateScopes(t *testing.T) {
	fixture := newAdoptFixture(t)
	mainSession := fixture.createRoot(t)
	featureSession := fixture.createRoot(t)
	if err := fixture.store.UpdateSession(fixture.mainMeta.ID, mainSession); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.UpdateSession(fixture.featureMeta.ID, featureSession); err != nil {
		t.Fatal(err)
	}

	fixture.adopt(t)
	fixture.assertNoLegacyReference(t)

	if scope, ok, err := fixture.pool.SessionScope(mainSession); err != nil || !ok || scope != project.MainScope {
		t.Fatalf("main session scope = %q, %t, %v", scope, ok, err)
	}
	if scope, ok, err := fixture.pool.SessionScope(featureSession); err != nil || !ok || scope != fixture.featureMeta.ID {
		t.Fatalf("feature session scope = %q, %t, %v", scope, ok, err)
	}
	if current, err := fixture.pool.CurrentSessionID(project.MainScope); err != nil || current != mainSession {
		t.Fatalf("main scope current = %q, %v", current, err)
	}
	if current, err := fixture.pool.CurrentSessionID(fixture.featureMeta.ID); err != nil || current != featureSession {
		t.Fatalf("feature scope current = %q, %v", current, err)
	}
}

func TestAdoptSessionsIsIdempotent(t *testing.T) {
	fixture := newAdoptFixture(t)
	session := fixture.createRoot(t)
	if err := fixture.store.UpdateSession(fixture.mainMeta.ID, session); err != nil {
		t.Fatal(err)
	}
	fixture.adopt(t)
	addedAt := fixture.refAddedAt(t, session)

	fixture.adopt(t)

	refs, err := fixture.pool.ListSessionRefs()
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs after repeated adoption = %#v, %v", refs, err)
	}
	if !refs[0].AddedAt.Equal(addedAt) {
		t.Fatalf("repeated adoption rewrote the reference: %v then %v", addedAt, refs[0].AddedAt)
	}
	if current, err := fixture.pool.CurrentSessionID(project.MainScope); err != nil || current != session {
		t.Fatalf("current after repeated adoption = %q, %v", current, err)
	}
}

func TestAdoptSessionsDropsMissingEntityAndKeepsArchivedOwnership(t *testing.T) {
	fixture := newAdoptFixture(t)
	archived := fixture.createRoot(t)
	if _, err := fixture.sessions.ArchiveIfExists(archived); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.UpdateSession(fixture.mainMeta.ID, "missing-session"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.UpdateSession(fixture.featureMeta.ID, archived); err != nil {
		t.Fatal(err)
	}

	fixture.adopt(t)
	fixture.assertNoLegacyReference(t)

	if refs, err := fixture.pool.ListSessionRefs(); err != nil || len(refs) != 1 || refs[0].SessionID != archived {
		t.Fatalf("refs = %#v, %v; want only the archived session", refs, err)
	}
	if current, err := fixture.pool.CurrentSessionID(fixture.featureMeta.ID); err != nil || current != "" {
		t.Fatalf("archived session became the scope current: %q, %v", current, err)
	}
}

func TestAdoptSessionsPointerConflictKeepsTheNewerRecord(t *testing.T) {
	t.Run("legacy reference is newer", func(t *testing.T) {
		fixture := newAdoptFixture(t)
		existing := fixture.createRoot(t)
		if err := fixture.pool.AddSession(project.MainScope, existing); err != nil {
			t.Fatal(err)
		}
		if err := fixture.pool.SetCurrentSession(project.MainScope, existing); err != nil {
			t.Fatal(err)
		}
		adopted := fixture.createRoot(t)
		if err := fixture.store.UpdateSession(fixture.mainMeta.ID, adopted); err != nil {
			t.Fatal(err)
		}
		fixture.adopt(t)
		if current, err := fixture.pool.CurrentSessionID(project.MainScope); err != nil || current != adopted {
			t.Fatalf("current = %q, %v; want the adopted session %q", current, err, adopted)
		}
	})

	t.Run("existing pointer is newer", func(t *testing.T) {
		fixture := newAdoptFixture(t)
		adopted := fixture.createRoot(t)
		if err := fixture.pool.AddSession(project.MainScope, adopted); err != nil {
			t.Fatal(err)
		}
		existing := fixture.createRoot(t)
		if err := fixture.pool.SetCurrentSession(project.MainScope, existing); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.UpdateSession(fixture.mainMeta.ID, adopted); err != nil {
			t.Fatal(err)
		}
		fixture.adopt(t)
		if current, err := fixture.pool.CurrentSessionID(project.MainScope); err != nil || current != existing {
			t.Fatalf("current = %q, %v; want the newer existing pointer %q", current, err, existing)
		}
	})
}

func TestAdoptSessionsReplacesDanglingScopePointer(t *testing.T) {
	fixture := newAdoptFixture(t)
	if err := fixture.pool.SetCurrentSession(project.MainScope, "dangling-session"); err != nil {
		t.Fatal(err)
	}
	adopted := fixture.createRoot(t)
	if err := fixture.store.UpdateSession(fixture.mainMeta.ID, adopted); err != nil {
		t.Fatal(err)
	}

	fixture.adopt(t)
	if current, err := fixture.pool.CurrentSessionID(project.MainScope); err != nil || current != adopted {
		t.Fatalf("current = %q, %v; want %q", current, err, adopted)
	}
}

func TestAdoptSessionsRejectsMissingDependencies(t *testing.T) {
	fixture := newAdoptFixture(t)
	if err := AdoptSessions(context.Background(), nil, fixture.projectRoot, fixture.pool, fixture.sessions); err == nil {
		t.Fatal("missing store was accepted")
	}
	if err := AdoptSessions(context.Background(), fixture.store, fixture.projectRoot, nil, fixture.sessions); err == nil {
		t.Fatal("missing session pool was accepted")
	}
	if err := AdoptSessions(context.Background(), fixture.store, fixture.projectRoot, fixture.pool, nil); err == nil {
		t.Fatal("missing session manager was accepted")
	}
	if err := AdoptSessions(context.Background(), fixture.store, fixture.projectRoot, fixture.pool, fixture.sessions); err != nil {
		t.Fatalf("adoption without legacy references = %v", err)
	}
}

func TestAdoptSessionsAfterLegacyMigration(t *testing.T) {
	repo := newGitRepo(t)
	data := filepath.Join(t.TempDir(), "data")
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", data)
	if err != nil {
		t.Fatal(err)
	}
	manager := sessions.NewManager(sessionfile.NewFileSessionStore(filepath.Join(data, "sessions")), filepath.Join(data, "current"), "")
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacySession := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(data, "worktrees", svc.RepositoryID(), "registry.json")
	if err := saveRegistry(legacyPath, registry{
		Version: 1,
		Entries: []registryEntry{{
			Path: svc.ProjectCodeRoot(), Branch: "main", SessionID: legacySession,
			CreatedAt: time.Now().Add(-time.Hour), LastUsedAt: time.Now(),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(data, "projects"), svc.ProjectIdentity().ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(context.Background(), legacyPath, store); err != nil {
		t.Fatal(err)
	}
	meta, err := store.Get(svc.ProjectCodeRoot())
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != legacySession {
		t.Fatalf("migrated main session = %q", meta.SessionID)
	}

	pool, err := project.OpenWithIdentity(svc.ProjectCodeRoot(), svc.ProjectIdentity(), project.NewFileStore(filepath.Join(data, "projects")))
	if err != nil {
		t.Fatal(err)
	}
	if err := AdoptSessions(context.Background(), store, svc.ProjectCodeRoot(), pool, manager); err != nil {
		t.Fatal(err)
	}
	if scope, ok, err := pool.SessionScope(legacySession); err != nil || !ok || scope != project.MainScope {
		t.Fatalf("migrated session scope = %q, %t, %v", scope, ok, err)
	}
	if current, err := pool.CurrentSessionID(project.MainScope); err != nil || current != legacySession {
		t.Fatalf("main scope current = %q, %v", current, err)
	}
	meta, err = store.Get(svc.ProjectCodeRoot())
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != "" {
		t.Fatalf("legacy reference was not cleared: %q", meta.SessionID)
	}
}

func (f *adoptFixture) refAddedAt(t *testing.T, sessionID string) time.Time {
	t.Helper()
	refs, err := f.pool.ListSessionRefs()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if ref.SessionID == sessionID {
			return ref.AddedAt
		}
	}
	t.Fatalf("reference for %q is missing", sessionID)
	return time.Time{}
}
