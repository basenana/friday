package main

import (
	"encoding/json"
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
	if _, err := os.Stat(filepath.Join(fridayDir, "workspace", "mcp")); err != nil {
		t.Fatalf("project MCP directory missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fridayDir, "agents")); err != nil {
		t.Fatalf("project agents directory missing: %v", err)
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
	if cfg.Model == nil || cfg.Model.Provider == "" || cfg.Model.Model == "" {
		t.Fatalf("generated model defaults are incomplete: %#v", cfg.Model)
	}
	var generated config.Config
	if err := json.Unmarshal(before, &generated); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	if generated.ImageModel == nil || generated.ImageModel.ContextWindow == 0 || generated.ImageModel.MaxTokens == 0 || generated.ImageModel.QPM == 0 {
		t.Fatalf("generated image_model template is incomplete: %#v", generated.ImageModel)
	}
	if generated.ImageModel.IsConfigured() {
		t.Fatalf("generated image_model template should remain inactive until filled: %#v", generated.ImageModel)
	}
	if cfg.ImageModel != nil {
		t.Fatalf("unnamed runtime image_model = %#v, want nil", cfg.ImageModel)
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
	if _, err := os.Stat(filepath.Join(home, ".friday", "agents")); err != nil {
		t.Fatalf("HOME agents directory missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "mcp")); err != nil {
		t.Fatalf("HOME MCP directory missing: %v", err)
	}
	for name := range workspace.DefaultContents {
		if _, err := os.Stat(filepath.Join(workspaceDir, name)); err != nil {
			t.Fatalf("HOME init did not create %s: %v", name, err)
		}
	}
}
