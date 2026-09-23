package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrateLegacyImportsEntriesAndRetainsSessionReferences(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "legacy", "registry.json")
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	writeLegacyRegistry(t, legacyPath, []registryEntry{{
		Path: checkout, Branch: "friday/login", ProjectID: "old-checkout-project", SessionID: "existing-session",
		CreatedAt: created, LastUsedAt: created.Add(time.Hour),
	}})
	store := newProjectWorktreeStore(t, filepath.Join(root, "projects"), "logical-project")

	if err := MigrateLegacy(context.Background(), legacyPath, store); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("migrated entries = %#v, want one", items)
	}
	got := items[0]
	if got.SessionID != "existing-session" {
		t.Fatalf("session reference = %q, want existing-session", got.SessionID)
	}
	if got.Branch != "friday/login" || got.Name != "checkout" {
		t.Fatalf("migrated metadata = %#v", got)
	}
	if !got.CreatedAt.Equal(created) || !got.LastUsedAt.Equal(created.Add(time.Hour)) {
		t.Fatalf("migrated timestamps = %s, %s", got.CreatedAt, got.LastUsedAt)
	}
	assertLegacyMigrationComplete(t, legacyPath, 1)
}

func TestMigrateLegacyCanonicalizesDuplicatePaths(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "checkout-link")
	if err := os.Symlink(checkout, alias); err != nil {
		t.Skipf("symlink: %v", err)
	}
	legacyPath := filepath.Join(root, "legacy", "registry.json")
	earlier := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)
	writeLegacyRegistry(t, legacyPath, []registryEntry{
		{Path: alias, Branch: "friday/old", SessionID: "session-old", CreatedAt: earlier, LastUsedAt: earlier},
		{Path: checkout, Branch: "friday/new", SessionID: "session-new", CreatedAt: later, LastUsedAt: later},
	})
	store := newProjectWorktreeStore(t, filepath.Join(root, "projects"), "logical-project")

	if err := MigrateLegacy(context.Background(), legacyPath, store); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("migrated entries = %#v, want one canonical checkout", items)
	}
	if got := items[0]; got.Branch != "friday/new" || got.SessionID != "session-new" || !got.CreatedAt.Equal(earlier) || !got.LastUsedAt.Equal(later) {
		t.Fatalf("canonical duplicate = %#v", got)
	}
}

func TestMigrateLegacyToleratesMissingSessionAndStaleCheckout(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "legacy", "registry.json")
	stale := filepath.Join(root, "removed", "checkout")
	writeLegacyRegistry(t, legacyPath, []registryEntry{{
		Path: stale, Branch: "friday/stale", SessionID: "missing-session",
	}})
	store := newProjectWorktreeStore(t, filepath.Join(root, "projects"), "logical-project")

	if err := MigrateLegacy(context.Background(), legacyPath, store); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	wantStale := filepath.Join(testCanonicalWorktreePath(t, root), "removed", "checkout")
	if len(items) != 1 || items[0].SessionID != "missing-session" || items[0].Path != wantStale {
		t.Fatalf("stale soft reference = %#v", items)
	}
}

func TestMigrateLegacyInterruptedMigrationResumesBeforeCompletion(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "legacy", "registry.json")
	createdOne := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	usedOne := createdOne.Add(2 * time.Hour)
	createdTwo := createdOne.Add(time.Hour)
	usedTwo := createdTwo.Add(3 * time.Hour)
	writeLegacyRegistry(t, legacyPath, []registryEntry{
		{Path: filepath.Join(root, "one"), Branch: "friday/one", CreatedAt: createdOne, LastUsedAt: usedOne},
		{Path: filepath.Join(root, "two"), Branch: "friday/two", CreatedAt: createdTwo, LastUsedAt: usedTwo},
	})
	base := newProjectWorktreeStore(t, filepath.Join(root, "projects"), "logical-project")
	interrupted := &migrationTestStore{Store: base, failAt: 2}

	if err := MigrateLegacy(context.Background(), legacyPath, interrupted); err == nil {
		t.Fatal("interrupted migration succeeded")
	}
	assertLegacyMigrationPending(t, legacyPath)
	if err := MigrateLegacy(context.Background(), legacyPath, base); err != nil {
		t.Fatalf("resume migration: %v", err)
	}
	items, err := base.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("entries after resume = %#v, want two", items)
	}
	if !items[0].CreatedAt.Equal(createdOne) || !items[0].LastUsedAt.Equal(usedOne) {
		t.Fatalf("retried first timestamps = %s, %s; want %s, %s", items[0].CreatedAt, items[0].LastUsedAt, createdOne, usedOne)
	}
	if !items[1].CreatedAt.Equal(createdTwo) || !items[1].LastUsedAt.Equal(usedTwo) {
		t.Fatalf("retried second timestamps = %s, %s; want %s, %s", items[1].CreatedAt, items[1].LastUsedAt, createdTwo, usedTwo)
	}
	assertLegacyMigrationComplete(t, legacyPath, 2)
}

func TestMigrateLegacyDoesNotMarkCompleteBeforeMetadataDirectorySync(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "legacy", "registry.json")
	checkout := filepath.Join(root, "checkout")
	writeLegacyRegistry(t, legacyPath, []registryEntry{{Path: checkout, Branch: "friday/sync"}})
	store := newProjectWorktreeStore(t, filepath.Join(root, "projects"), "logical-project").(*fileStore)
	metadataDir := filepath.Dir(store.metadataPath(worktreeID(checkout)))
	store.syncDir = func(path string) error {
		if path == metadataDir {
			return errors.New("injected directory sync failure")
		}
		return nil
	}

	if err := MigrateLegacy(context.Background(), legacyPath, store); err == nil {
		t.Fatal("migration succeeded before metadata directory was durable")
	}
	if _, err := os.Stat(store.metadataPath(worktreeID(checkout))); err != nil {
		t.Fatalf("metadata rename did not precede directory sync: %v", err)
	}
	assertLegacyMigrationPending(t, legacyPath)
}

func TestMigrateLegacyRepeatedMigrationIsNoOp(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "legacy", "registry.json")
	writeLegacyRegistry(t, legacyPath, []registryEntry{{Path: filepath.Join(root, "one"), Branch: "friday/one"}})
	counted := &migrationTestStore{Store: newProjectWorktreeStore(t, filepath.Join(root, "projects"), "logical-project")}

	if err := MigrateLegacy(context.Background(), legacyPath, counted); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(context.Background(), legacyPath, counted); err != nil {
		t.Fatal(err)
	}
	if counted.calls != 1 {
		t.Fatalf("Ensure calls = %d, want one across repeated migration", counted.calls)
	}
}

type migrationTestStore struct {
	Store
	calls  int
	failAt int
}

func (s *migrationTestStore) Ensure(meta Metadata) (Metadata, error) {
	s.calls++
	if s.failAt > 0 && s.calls == s.failAt {
		return Metadata{}, errors.New("injected migration interruption")
	}
	return s.Store.Ensure(meta)
}

func writeLegacyRegistry(t *testing.T, path string, entries []registryEntry) {
	t.Helper()
	if err := saveRegistry(path, registry{Version: 1, Entries: entries}); err != nil {
		t.Fatal(err)
	}
}

func assertLegacyMigrationPending(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if raw := doc["migrated"]; len(raw) > 0 && string(raw) != "false" {
		t.Fatalf("interrupted registry marked migrated: %s", data)
	}
}

func assertLegacyMigrationComplete(t *testing.T, path string, wantEntries int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("legacy registry was not retained: %v", err)
	}
	var doc struct {
		Migrated bool            `json:"migrated"`
		Entries  []registryEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Migrated || len(doc.Entries) != wantEntries {
		t.Fatalf("retained legacy registry = %s", data)
	}
}
