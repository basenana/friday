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

func TestProjectIDPrefixSanitizesPathUnsafeNames(t *testing.T) {
	if got, want := ProjectIDPrefix(" repo\\name "), "repo_name"; got != want {
		t.Fatalf("ProjectIDPrefix() = %q, want %q", got, want)
	}
}

func TestOpenKeepsPathProjectIdentityForNonGitRoot(t *testing.T) {
	root := t.TempDir()
	canonical, err := CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Open(root, NewFileStore(filepath.Join(t.TempDir(), "projects")))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.ID(), ProjectID(canonical); got != want {
		t.Fatalf("project ID = %q, want path identity %q", got, want)
	}
}

func TestOpenWithIdentityPersistsVersion2ProjectMetadata(t *testing.T) {
	storeRoot := filepath.Join(t.TempDir(), "projects")
	identity := Identity{ID: "repository-a1b2c3", Name: "repository", Repository: "/repos/repository/.git"}
	p, err := OpenWithIdentity(t.TempDir(), identity, NewFileStore(storeRoot))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(storeRoot, p.ID(), "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Version != 2 || meta.ID != identity.ID || meta.Name != identity.Name || meta.Repository != identity.Repository {
		t.Fatalf("metadata = %+v, want version-2 identity %+v", meta, identity)
	}
}

func TestFileStoreMigrateIdentityPreservesWholeLegacyProject(t *testing.T) {
	root := t.TempDir()
	canonical, err := CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(t.TempDir(), "projects")
	store := NewFileStore(projects)
	legacy, err := Open(root, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.SetCodebaseEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := legacy.AddSession("session-old"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SetCurrentSession("session-old"); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1_700_000_000, 0)
	if _, err := legacy.AppendUserHistory("legacy prompt", "session-old", when); err != nil {
		t.Fatal(err)
	}
	legacyDir := filepath.Join(projects, legacy.ID())
	for path, content := range map[string]string{
		filepath.Join(legacyDir, "codebase", "INDEX.md"): "legacy index\n",
		filepath.Join(legacyDir, "sandbox.json"):         `{"version":1,"allow":["gofmt"]}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	identity := Identity{ID: "stable-repository-a1b2c3", Name: "repository", Repository: filepath.Join(canonical, ".git")}
	if err := store.MigrateIdentity(root, identity); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateIdentity(root, identity); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	migrated, err := OpenWithIdentity(root, identity, store)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := migrated.CodebaseEnabled(); err != nil || !enabled {
		t.Fatalf("Codebase enabled = %v, %v", enabled, err)
	}
	if current, err := migrated.CurrentSessionID(); err != nil || current != "session-old" {
		t.Fatalf("current = %q, %v", current, err)
	}
	refs, err := migrated.ListSessionRefs()
	if err != nil || len(refs) != 1 || refs[0].SessionID != "session-old" {
		t.Fatalf("refs = %#v, %v", refs, err)
	}
	history, err := migrated.LoadUserHistory()
	if err != nil || len(history) != 1 || history[0].Text != "legacy prompt" {
		t.Fatalf("history = %#v, %v", history, err)
	}
	stableDir := filepath.Join(projects, identity.ID)
	got, err := os.ReadFile(filepath.Join(stableDir, "codebase", "INDEX.md"))
	if err != nil || string(got) != "legacy index\n" {
		t.Fatalf("migrated Codebase index = %q, %v", got, err)
	}
	var grants struct {
		Version int      `json:"version"`
		Allow   []string `json:"allow"`
	}
	got, err = os.ReadFile(filepath.Join(stableDir, "sandbox.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &grants); err != nil || grants.Version != 1 || len(grants.Allow) != 1 || grants.Allow[0] != "gofmt" {
		t.Fatalf("migrated sandbox grants = %#v, %v", grants, err)
	}
	if _, err := os.Stat(legacyDir); err != nil {
		t.Fatalf("legacy project should remain as a retry/recovery source: %v", err)
	}
}

func TestOpenMigratesMatchingVersion1ProjectMetadata(t *testing.T) {
	root := t.TempDir()
	canonical, err := CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := filepath.Join(t.TempDir(), "projects")
	id := ProjectID(canonical)
	path := filepath.Join(storeRoot, id, "project.json")
	legacy := Metadata{Version: 1, ID: id, Root: canonical, CodebaseEnabled: true, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour)}
	if err := writeAtomicJSON(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, NewFileStore(storeRoot)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var migrated Metadata
	if err := json.Unmarshal(data, &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.Version != 2 || migrated.Repository != canonical || !migrated.CodebaseEnabled {
		t.Fatalf("migrated metadata = %+v", migrated)
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
