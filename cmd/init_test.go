package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/workspace"
)

func TestRunInitCreatesMinimalProjectWorkspace(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	if err := runInit(project); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}
	fridayDir := filepath.Join(project, ".friday")
	configPath := filepath.Join(fridayDir, "config.json")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fridayDir, "workspace", "skills")); err != nil {
		t.Fatalf("project skills directory missing: %v", err)
	}
	for name := range workspace.DefaultContents {
		if _, err := os.Stat(filepath.Join(fridayDir, "workspace", name)); !os.IsNotExist(err) {
			t.Fatalf("project init unexpectedly generated %s", name)
		}
	}

	cfg, err := config.LoadForDir("", project)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspacePath() != filepath.Join(fridayDir, "workspace") {
		t.Fatalf("WorkspacePath() = %q", cfg.WorkspacePath())
	}
	if cfg.DataDirPath() != filepath.Join(home, ".friday") {
		t.Fatalf("DataDirPath() = %q", cfg.DataDirPath())
	}

	if err := runInit(project); err != nil {
		t.Fatalf("second runInit() error = %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("repeated project init overwrote config")
	}
}

func TestRunInitAtHomeCreatesDefaultWorkspaceFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := runInit(home); err != nil {
		t.Fatalf("runInit(HOME) error = %v", err)
	}
	workspaceDir := filepath.Join(home, ".friday", "workspace")
	for name := range workspace.DefaultContents {
		if _, err := os.Stat(filepath.Join(workspaceDir, name)); err != nil {
			t.Fatalf("HOME init did not create %s: %v", name, err)
		}
	}
}
