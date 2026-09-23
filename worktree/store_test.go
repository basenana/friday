package worktree

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectWorktreeStoreWritesProjectOwnedMetadata(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	store := newProjectWorktreeStore(t, projects, "project-123")
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	meta, err := store.Ensure(Metadata{
		ID: "checkout-1", Name: "checkout", Path: checkout, Branch: "friday/checkout",
		SessionID: "session-1", CreatedAt: created, LastUsedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := meta.Path, testCanonicalWorktreePath(t, checkout); got != want {
		t.Fatalf("metadata path = %q, want %q", got, want)
	}

	path := filepath.Join(projects, "project-123", "worktrees", "checkout-1", "worktree.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read project-owned metadata: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "id", "name", "path", "branch", "session_id", "created_at", "last_used_at"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("persisted metadata missing %q: %s", key, data)
		}
	}
	if len(fields) != 8 {
		t.Fatalf("persisted metadata has dynamic fields: %s", data)
	}
}

func TestProjectWorktreeStoreReplacesMetadataAtomically(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	store := newProjectWorktreeStore(t, projects, "project-123")
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	if _, err := store.Ensure(Metadata{ID: "checkout-1", Name: "checkout", Path: checkout, Branch: "friday/checkout", CreatedAt: created, LastUsedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSession("checkout-1", "session-2"); err != nil {
		t.Fatal(err)
	}
	meta, err := store.Get("checkout-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := meta.SessionID, "session-2"; got != want {
		t.Fatalf("session id = %q, want %q", got, want)
	}
	if got, want := meta.CreatedAt, created; !got.Equal(want) {
		t.Fatalf("created at = %s, want %s", got, want)
	}

	dir := filepath.Join(projects, "project-123", "worktrees", "checkout-1")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "worktree.json" {
		t.Fatalf("metadata replacement left temporary files: %#v", entries)
	}
}

func TestProjectWorktreeStoreSyncsMetadataHierarchyAfterRename(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	projects := filepath.Join(dataDir, "projects")
	store := newProjectWorktreeStore(t, projects, "project-123").(*fileStore)
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	var synced []string
	store.syncDir = func(path string) error {
		synced = append(synced, path)
		return nil
	}

	if _, err := store.Ensure(Metadata{ID: "checkout-1", Name: "checkout", Path: checkout, Branch: "friday/checkout"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(projects, "project-123", "worktrees", "checkout-1"),
		filepath.Join(projects, "project-123", "worktrees"),
		filepath.Join(projects, "project-123"),
		projects,
		dataDir,
	}
	if len(synced) != len(want) {
		t.Fatalf("synced directories = %#v, want %#v", synced, want)
	}
	for i := range want {
		if synced[i] != want[i] {
			t.Fatalf("synced directories = %#v, want %#v", synced, want)
		}
	}
}

func TestProjectWorktreeStoreKeepsOneEntryForCanonicalPath(t *testing.T) {
	store := newProjectWorktreeStore(t, filepath.Join(t.TempDir(), "projects"), "project-123")
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "checkout-link")
	if err := os.Symlink(checkout, alias); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	if _, err := store.Ensure(Metadata{ID: "checkout-1", Name: "checkout", Path: alias, Branch: "friday/checkout"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(Metadata{ID: "checkout-2", Name: "other", Path: checkout, Branch: "friday/other"}); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("metadata entries = %#v, want one canonical path", items)
	}
	if got, want := items[0].ID, "checkout-1"; got != want {
		t.Fatalf("canonical entry id = %q, want %q", got, want)
	}
	if got, want := items[0].Path, testCanonicalWorktreePath(t, checkout); got != want {
		t.Fatalf("canonical entry path = %q, want %q", got, want)
	}
}

func testCanonicalWorktreePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(resolved)
}

func TestProjectWorktreeStoreResolvesNameAndBranch(t *testing.T) {
	store := newProjectWorktreeStore(t, filepath.Join(t.TempDir(), "projects"), "project-123")
	for _, meta := range []Metadata{
		{ID: "checkout-1", Name: "login", Path: filepath.Join(t.TempDir(), "login"), Branch: "friday/login"},
		{ID: "checkout-2", Name: "logout", Path: filepath.Join(t.TempDir(), "logout"), Branch: "friday/logout"},
	} {
		if err := os.Mkdir(meta.Path, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Ensure(meta); err != nil {
			t.Fatal(err)
		}
	}
	byName, err := store.Get("login")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := byName.ID, "checkout-1"; got != want {
		t.Fatalf("name resolution = %q, want %q", got, want)
	}
	byBranch, err := store.Get("friday/logout")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := byBranch.ID, "checkout-2"; got != want {
		t.Fatalf("branch resolution = %q, want %q", got, want)
	}
}

func TestProjectWorktreeStoreUpdatesMutableFieldsWithoutDroppingAssociation(t *testing.T) {
	store := newProjectWorktreeStore(t, filepath.Join(t.TempDir(), "projects"), "project-123")
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	if _, err := store.Ensure(Metadata{ID: "checkout-1", Name: "old-name", Path: checkout, Branch: "friday/old-name", SessionID: "session-1", CreatedAt: created, LastUsedAt: created}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Ensure(Metadata{ID: "checkout-1", Name: "new-name", Path: checkout, Branch: "friday/new-name"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := updated.Name, "new-name"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if got, want := updated.Branch, "friday/new-name"; got != want {
		t.Fatalf("branch = %q, want %q", got, want)
	}
	if got, want := updated.SessionID, "session-1"; got != want {
		t.Fatalf("session id = %q, want preserved %q", got, want)
	}
	if got, want := updated.CreatedAt, created; !got.Equal(want) {
		t.Fatalf("created at = %s, want %s", got, want)
	}
}

func TestProjectWorktreeStoreCanonicalizesMissingLeafBelowAliasedParent(t *testing.T) {
	store := newProjectWorktreeStore(t, filepath.Join(t.TempDir(), "projects"), "project-123")
	realParent := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	aliasedMissing := filepath.Join(aliasParent, "not-created")
	canonicalMissing := filepath.Join(realParent, "not-created")
	if _, err := store.Ensure(Metadata{ID: "checkout-1", Name: "checkout", Path: aliasedMissing, Branch: "friday/checkout"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(Metadata{ID: "checkout-2", Name: "other", Path: canonicalMissing, Branch: "friday/other"}); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("metadata entries = %#v, want one canonical path", items)
	}
	if got, want := items[0].Path, filepath.Join(testCanonicalWorktreePath(t, realParent), "not-created"); got != want {
		t.Fatalf("canonical missing path = %q, want %q", got, want)
	}
}

func TestProjectWorktreeStoreRejectsUnsafeProjectID(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	if _, err := NewStore(projects, "../outside"); err == nil {
		t.Fatal("NewStore accepted a traversal project ID")
	}
	if _, err := os.Stat(projects); !os.IsNotExist(err) {
		t.Fatalf("unsafe store construction changed projects directory: %v", err)
	}
}

func newProjectWorktreeStore(t *testing.T, projects, projectID string) Store {
	t.Helper()
	store, err := NewStore(projects, projectID)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
