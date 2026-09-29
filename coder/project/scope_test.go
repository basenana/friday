package project

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

var _ sessions.RootCatalog = (*Manager)(nil)

func newScopedTestProject(t *testing.T, data string) (*Project, Store) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(filepath.Join(data, "projects"))
	project, err := Open(root, store)
	if err != nil {
		t.Fatal(err)
	}
	return project, store
}

func createScopedRoot(t *testing.T, manager *Manager) string {
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

func TestScopedManagerIsolatesSessionsAndCurrentPointer(t *testing.T) {
	data := t.TempDir()
	project, store := newScopedTestProject(t, data)
	manager := sessions.NewManager(sessionfile.NewFileSessionStore(filepath.Join(data, "sessions")), filepath.Join(data, "current"), "")
	main := NewManager(project, manager)
	feature := NewScopedManager(project, manager, "feature-1")

	mainID := createScopedRoot(t, main)
	featureID := createScopedRoot(t, feature)
	if mainID == featureID {
		t.Fatal("scopes shared one session entity")
	}
	if err := main.Activate(mainID); err != nil {
		t.Fatal(err)
	}
	if err := feature.Activate(featureID); err != nil {
		t.Fatal(err)
	}

	if current, err := main.CurrentID(); err != nil || current != mainID {
		t.Fatalf("main current = %q, %v", current, err)
	}
	if current, err := feature.CurrentID(); err != nil || current != featureID {
		t.Fatalf("feature current = %q, %v", current, err)
	}
	mainItems, err := main.List(true)
	if err != nil || len(mainItems) != 1 || mainItems[0].ID != mainID {
		t.Fatalf("main list = %+v, %v", mainItems, err)
	}
	featureItems, err := feature.List(true)
	if err != nil || len(featureItems) != 1 || featureItems[0].ID != featureID {
		t.Fatalf("feature list = %+v, %v", featureItems, err)
	}
	if _, err := feature.OpenRoot(context.Background(), mainID, nil); err == nil {
		t.Fatal("feature scope opened a main-scope session")
	}
	if _, err := main.OpenRoot(context.Background(), featureID, nil); err == nil {
		t.Fatal("main scope opened a feature-scope session")
	}
	if _, err := feature.Resolve(mainID); err == nil {
		t.Fatal("feature scope resolved a main-scope session")
	}

	if _, err := os.Stat(filepath.Join(data, "projects", project.ID(), "current")); err != nil {
		t.Fatalf("main current pointer missing: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(data, "projects", project.ID(), "current.feature-1"))
	if err != nil {
		t.Fatalf("feature current pointer missing: %v", err)
	}
	if got := string(raw); got != featureID+"\n" {
		t.Fatalf("feature current pointer = %q", got)
	}
	if current, err := store.Current(project.ID(), MainScope); err != nil || current != mainID {
		t.Fatalf("main pointer changed with a feature scope: %q, %v", current, err)
	}
}

func TestScopeRejectsPathTraversalAndKeepsLegacyRefsInMainScope(t *testing.T) {
	data := t.TempDir()
	project, store := newScopedTestProject(t, data)
	manager := sessions.NewManager(sessionfile.NewFileSessionStore(filepath.Join(data, "sessions")), filepath.Join(data, "current"), "")
	main := NewManager(project, manager)

	if _, _, err := manager.GetOrCreateDetachedByID("legacy-session"); err != nil {
		t.Fatal(err)
	}
	// Version-1 references written before scopes existed carry no scope field.
	if err := writeAtomicJSON(filepath.Join(data, "projects", project.ID(), "sessions", "legacy-session.json"),
		SessionRef{Version: 1, SessionID: "legacy-session"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if scope, ok, err := project.SessionScope("legacy-session"); err != nil || !ok || scope != MainScope {
		t.Fatalf("legacy reference scope = %q, %t, %v", scope, ok, err)
	}
	if has, err := main.Contains("legacy-session"); err != nil || !has {
		t.Fatalf("main scope contains legacy reference = %t, %v", has, err)
	}
	if has, err := NewScopedManager(project, manager, "feature-1").Contains("legacy-session"); err != nil || has {
		t.Fatalf("feature scope contains legacy reference = %t, %v", has, err)
	}

	for _, scope := range []string{"../escape", "a/b", ".."} {
		if _, err := store.Current(project.ID(), scope); err == nil {
			t.Fatalf("scope %q was accepted", scope)
		}
		if err := store.SetCurrent(project.ID(), scope, "legacy-session"); err == nil {
			t.Fatalf("scope %q was accepted for writing", scope)
		}
		if err := project.AddSession(scope, "legacy-session"); err == nil {
			t.Fatalf("scope %q was accepted as a reference scope", scope)
		}
	}
}

func TestScopedRefRoundTripPersistsScope(t *testing.T) {
	data := t.TempDir()
	project, store := newScopedTestProject(t, data)
	if err := project.AddSession("feature-1", "session-a"); err != nil {
		t.Fatal(err)
	}
	refs, err := store.ListRefs(project.ID())
	if err != nil || len(refs) != 1 || refs[0].Scope != "feature-1" {
		t.Fatalf("refs = %#v, %v", refs, err)
	}
	raw, err := os.ReadFile(filepath.Join(data, "projects", project.ID(), "sessions", "session-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["scope"] != "feature-1" {
		t.Fatalf("persisted reference = %s", raw)
	}
}
