package project

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

func openTestProject(t *testing.T, root, data string, manager *sessions.Manager) (*Project, *Manager) {
	t.Helper()
	project, err := Open(root, NewFileStore(filepath.Join(data, "projects")))
	if err != nil {
		t.Fatal(err)
	}
	return project, NewManager(project, manager)
}

func TestCanonicalRootAndProjectID(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "same")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	realCanonical, err := CanonicalRoot(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	linkCanonical, err := CanonicalRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	if realCanonical != linkCanonical || ProjectID(realCanonical) != ProjectID(linkCanonical) {
		t.Fatalf("symlink identity mismatch: %q/%q", realCanonical, linkCanonical)
	}

	otherParent := t.TempDir()
	other := filepath.Join(otherParent, "same")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	otherCanonical, _ := CanonicalRoot(other)
	if ProjectID(realCanonical) == ProjectID(otherCanonical) {
		t.Fatal("same basename at different paths produced the same project ID")
	}
}

func TestProjectCatalogScopesRootsAndCurrent(t *testing.T) {
	data := t.TempDir()
	rootA := filepath.Join(t.TempDir(), "repo")
	rootB := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(rootA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootB, 0o755); err != nil {
		t.Fatal(err)
	}
	store := sessionfile.NewFileSessionStore(filepath.Join(data, "sessions"))
	base := sessions.NewManager(store, filepath.Join(data, "legacy-current"), "")
	_, managerA := openTestProject(t, rootA, data, base)
	_, managerB := openTestProject(t, rootB, data, base)

	lifecycle, err := managerA.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := lifecycle.RootID()
	_ = lifecycle.Close()
	if err := managerA.Activate(id); err != nil {
		t.Fatal(err)
	}
	if current, err := managerA.CurrentID(); err != nil || current != id {
		t.Fatalf("current = %q, %v", current, err)
	}
	if _, err := managerB.OpenRoot(context.Background(), id, nil); err == nil {
		t.Fatal("other project opened an unreferenced session")
	}
	items, err := managerA.List(true)
	if err != nil || len(items) != 1 || items[0].ID != id {
		t.Fatalf("project list = %+v, %v", items, err)
	}
	if err := store.Delete(id); err != nil {
		t.Fatal(err)
	}
	items, err = managerA.List(true)
	if err != nil || len(items) != 0 {
		t.Fatalf("dangling ref should be ignored: %+v, %v", items, err)
	}
	if current, err := managerA.CurrentID(); err != nil || current != "" {
		t.Fatalf("dangling current = %q, %v", current, err)
	}
}

func TestCodebaseEnabledDefaultsFalseAndPersists(t *testing.T) {
	data := t.TempDir()
	root := t.TempDir()
	store := NewFileStore(filepath.Join(data, "projects"))
	p, err := Open(root, store)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := p.CodebaseEnabled(); err != nil || enabled {
		t.Fatalf("default enabled = %v, %v", enabled, err)
	}

	if err := p.SetCodebaseEnabled(true); err != nil {
		t.Fatal(err)
	}
	if enabled, err := p.CodebaseEnabled(); err != nil || !enabled {
		t.Fatalf("enabled = %v, %v", enabled, err)
	}

	reopened, err := Open(root, store)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := reopened.CodebaseEnabled(); err != nil || !enabled {
		t.Fatalf("reopened enabled = %v, %v", enabled, err)
	}
}

func TestSetCodebaseEnabledIsAtomicAndIdempotent(t *testing.T) {
	data := t.TempDir()
	root := t.TempDir()
	store := NewFileStore(filepath.Join(data, "projects"))
	p, err := Open(root, store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "projects", p.ID(), "project.json")
	beforeData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var before Metadata
	if err := json.Unmarshal(beforeData, &before); err != nil {
		t.Fatal(err)
	}

	if err := p.SetCodebaseEnabled(false); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(beforeData) {
		t.Fatal("idempotent disable rewrote project metadata")
	}

	time.Sleep(time.Millisecond)
	if err := p.SetCodebaseEnabled(true); err != nil {
		t.Fatal(err)
	}
	afterData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var after Metadata
	if err := json.Unmarshal(afterData, &after); err != nil {
		t.Fatal(err)
	}
	if !after.CodebaseEnabled || !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("metadata not updated: %+v", after)
	}
	if after.Version != before.Version || after.ID != before.ID || after.Root != before.Root || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("unrelated metadata changed: before=%+v after=%+v", before, after)
	}

	corrupt := []byte("{broken\n")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCodebaseEnabled(false); err == nil {
		t.Fatal("expected corrupt metadata error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(corrupt) {
		t.Fatal("corrupt metadata was partially rewritten")
	}
}

func TestSetCodebaseEnabledPreservesUnknownMetadataFields(t *testing.T) {
	data := t.TempDir()
	root := t.TempDir()
	store := NewFileStore(filepath.Join(data, "projects"))
	p, err := Open(root, store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "projects", p.ID(), "project.json")
	var raw map[string]any
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &raw); err != nil {
		t.Fatal(err)
	}
	raw["future_field"] = map[string]any{"keep": true}
	contents, err = json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCodebaseEnabled(true); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = nil
	if err := json.Unmarshal(contents, &raw); err != nil {
		t.Fatal(err)
	}
	future, ok := raw["future_field"].(map[string]any)
	if !ok || future["keep"] != true {
		t.Fatalf("unknown metadata field was lost: %s", contents)
	}
}

func TestAssociatedSessionIsNotProjectRootAndCascades(t *testing.T) {
	data := t.TempDir()
	root := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(data, "sessions"))
	base := sessions.NewManager(store, filepath.Join(data, "legacy-current"), "")
	_, manager := openTestProject(t, root, data, base)
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rootID := lifecycle.RootID()
	child, created, err := lifecycle.GetOrCreateAssociated(context.Background(), sessions.AssociatedSpec{Key: "proposal/p1/self"})
	if err != nil || !created {
		t.Fatalf("associated = %v, created=%v, err=%v", child, created, err)
	}
	items, err := manager.List(false)
	if err != nil || len(items) != 1 || items[0].ID != rootID {
		t.Fatalf("associated session leaked into project list: %+v, %v", items, err)
	}
	_ = lifecycle.Close()
	if err := manager.DeleteRoot(rootID); err != nil {
		t.Fatal(err)
	}
	if exists, _ := base.Exists(child.ID); exists {
		t.Fatal("associated session survived root deletion")
	}
}
