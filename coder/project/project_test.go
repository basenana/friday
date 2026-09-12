package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
