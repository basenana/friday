package tui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/actor"
	codercmds "github.com/basenana/friday/coder/commands"
	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

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
	registry := actor.NewRegistry(base, cfg, registryConfig)
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
