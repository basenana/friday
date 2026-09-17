package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/actor"
	codercmds "github.com/basenana/friday/coder/commands"
	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

func newLoadedProjectTestModel(t *testing.T) (*model, *projectpkg.Manager, *sessionfile.FileSessionStore) {
	t.Helper()
	data := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = data
	cfg.Workspace = filepath.Join(data, "workspace")
	cfg.Memory.Enabled = false
	store := sessionfile.NewFileSessionStore(cfg.SessionsPath())
	base := sessions.NewManager(store, filepath.Join(data, "legacy-current"), "")
	project, err := projectpkg.Open(t.TempDir(), projectpkg.NewFileStore(cfg.ProjectsPath()))
	if err != nil {
		t.Fatal(err)
	}
	manager := projectpkg.NewManager(project, base)
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = lifecycle.Close()

	registryConfig := actor.DefaultRegistryConfig()
	registryConfig.Catalog = manager
	registryConfig.Workdir = project.Root()
	registry, err := actor.NewRegistry(base, cfg, registryConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.ShutdownAll)
	commands := codercmds.NewRegistry()
	codercmds.RegisterAll(commands)
	m := loadingModelAt(base, registry, commands, cfg, "", project.Root())
	m.projectMgr = manager
	m.runtime = manager
	loaded := m.loadInitialSession()()
	updated, _ := m.Update(loaded)
	m = updated.(*model)
	if m.fatalErr != nil {
		t.Fatalf("load project model: %v", m.fatalErr)
	}
	t.Cleanup(m.closeFeed)
	return m, manager, store
}

func TestProjectClearCreatesReferencedRoot(t *testing.T) {
	data := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = data
	cfg.Workspace = filepath.Join(data, "workspace")
	cfg.Memory.Enabled = false
	store := sessionfile.NewFileSessionStore(cfg.SessionsPath())
	base := sessions.NewManager(store, filepath.Join(data, "legacy-current"), "")
	project, err := projectpkg.Open(t.TempDir(), projectpkg.NewFileStore(cfg.ProjectsPath()))
	if err != nil {
		t.Fatal(err)
	}
	manager := projectpkg.NewManager(project, base)
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	initialID := lifecycle.RootID()
	_ = lifecycle.Close()
	if err := manager.Activate(initialID); err != nil {
		t.Fatal(err)
	}

	registryConfig := actor.DefaultRegistryConfig()
	registryConfig.Catalog = manager
	registryConfig.Workdir = project.Root()
	registry, err := actor.NewRegistry(base, cfg, registryConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.ShutdownAll)
	commands := codercmds.NewRegistry()
	codercmds.RegisterAll(commands)
	m, err := initialModel(base, registry, commands, cfg, initialID)
	if err != nil {
		t.Fatal(err)
	}
	m.projectMgr = manager
	m.workdir = project.Root()

	if handled, _ := m.applySessionAction(codercmds.ClearSessionAction{SessionID: "ignored"}); !handled {
		t.Fatal("clear action was not handled")
	}
	if m.sessionID == initialID {
		t.Fatal("clear did not switch to a new root")
	}
	items, err := manager.List(true)
	if err != nil || len(items) != 2 {
		t.Fatalf("project sessions = %+v, %v", items, err)
	}
	current, err := manager.CurrentID()
	if err != nil || current != m.sessionID {
		t.Fatalf("project current = %q, want %q, err=%v", current, m.sessionID, err)
	}
}

func TestProjectPromptHistoryPersistsAcrossSessions(t *testing.T) {
	m, manager, _ := newLoadedProjectTestModel(t)
	if _, err := manager.AppendUserHistory("first prompt", m.sessionID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendUserHistory("second prompt", m.sessionID, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Reload the project model to exercise startup loading from user_history.jsonl.
	m.closeFeed()
	loaded := m.loadInitialSession()()
	updated, _ := m.Update(loaded)
	m = updated.(*model)
	if len(m.promptHistory) != 2 || m.promptHistory[0] != "first prompt" || m.promptHistory[1] != "second prompt" {
		t.Fatalf("loaded prompt history = %#v", m.promptHistory)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = updated.(*model)
	if got := m.textarea.Value(); got != "second prompt" {
		t.Fatalf("up restored %q", got)
	}
	m.textarea.Reset()
	m.historyIndex = -1

	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	newID := lifecycle.RootID()
	_ = lifecycle.Close()
	if _, err := m.switchSession(newID); err != nil {
		t.Fatal(err)
	}
	if len(m.promptHistory) != 2 || m.promptHistory[0] != "first prompt" || m.promptHistory[1] != "second prompt" {
		t.Fatalf("history after session switch = %#v", m.promptHistory)
	}

	m.textarea.SetValue("/unknown")
	_, _ = m.submitComposer()
	m.textarea.SetValue("/unknown")
	_, _ = m.submitComposer()
	m.textarea.SetValue("queued prompt")
	_, _ = m.queueComposer()
	m.handleCustomEvent(events.NewEvent(events.KindCustom, "accepted-queued").WithName(events.CustomInputAccepted).WithPayload(events.InputAcceptedBody{
		TurnID: "accepted-queued", Text: "queued prompt",
	}))
	if len(m.promptHistory) != 4 {
		t.Fatalf("accepted event duplicated project prompt history: %#v", m.promptHistory)
	}

	entries, err := manager.LoadUserHistory()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first prompt", "second prompt", "/unknown", "queued prompt"}
	if len(entries) != len(want) {
		t.Fatalf("persisted history length = %d, want %d: %#v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i].Text != want[i] {
			t.Fatalf("history[%d] = %q, want %q", i, entries[i].Text, want[i])
		}
	}
}

func TestProjectPromptHistoryDoesNotBackfillSessionMessages(t *testing.T) {
	m, manager, store := newLoadedProjectTestModel(t)
	if err := store.AppendMessages(m.sessionID, types.Message{Role: types.RoleUser, Content: "old session prompt", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}

	m.closeFeed()
	loaded := m.loadInitialSession()()
	updated, _ := m.Update(loaded)
	m = updated.(*model)
	if len(m.promptHistory) != 0 {
		t.Fatalf("session messages backfilled project history: %#v", m.promptHistory)
	}
	entries, err := manager.LoadUserHistory()
	if err != nil || len(entries) != 0 {
		t.Fatalf("stored project history = %#v, %v", entries, err)
	}
}
