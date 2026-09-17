package configtools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/workspace"
)

func TestFileStoreAgentCRUD(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents")
	store := NewFileStore([]string{root}, nil, func(name string) bool { return name == "known" })
	created, err := store.CreateAgent(AgentInput{Name: "writer", Description: "Writes", Instructions: "Write clearly.", Model: "known", MaxLoopTimes: 7})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "writer" || created.SystemPrompt != "Write clearly." {
		t.Fatalf("created = %#v", created)
	}
	updated, err := store.UpdateAgent("writer", map[string]any{"description": "Better writer", "instructions": "Revise carefully."})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "Better writer" || updated.SystemPrompt != "Revise carefully." || updated.Model != "known" {
		t.Fatalf("updated = %#v", updated)
	}
	if err := store.DeleteAgent("writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "writer")); !os.IsNotExist(err) {
		t.Fatalf("agent directory still exists: %v", err)
	}
}

func TestFileStoreMCPCRUDAndRedaction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mcp")
	store := NewFileStore(nil, []workspace.ResourceRoot{{Path: root, Scope: workspace.ScopeProject, Writable: true}}, nil)
	created, err := store.CreateMCP("remote", map[string]any{
		"type": "streamable-http", "url": "https://user:pass@example.com/mcp?token=secret",
		"headers": map[string]any{"Authorization": "Bearer secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Project || created.Name != "remote" {
		t.Fatalf("created = %#v", created)
	}
	redacted := RedactedMCP(created)
	if redacted["url"] != "https://example.com/mcp" {
		t.Fatalf("redacted URL = %#v", redacted["url"])
	}
	headers := redacted["headers"].(map[string]any)
	if headers["Authorization"] != "[redacted]" {
		t.Fatalf("redacted headers = %#v", headers)
	}
	updated, err := store.UpdateMCP("remote", map[string]any{"disabled": true})
	if err != nil || !updated.Disabled || updated.URL == "" {
		t.Fatalf("updated = %#v, err=%v", updated, err)
	}
	if err := store.DeleteMCP("remote"); err != nil {
		t.Fatal(err)
	}
	if configs, err := store.ListMCP(); err != nil || len(configs) != 0 {
		t.Fatalf("configs after delete = %#v, err=%v", configs, err)
	}
}

func TestFileStoreRejectsInheritedDelete(t *testing.T) {
	base := t.TempDir()
	homeAgents := filepath.Join(base, "home-agents")
	projectAgents := filepath.Join(base, "project-agents")
	homeStore := NewFileStore([]string{homeAgents}, nil, nil)
	if _, err := homeStore.CreateAgent(AgentInput{Name: "shared", Instructions: "Shared."}); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore([]string{homeAgents, projectAgents}, nil, nil)
	if err := store.DeleteAgent("shared"); err == nil {
		t.Fatal("expected inherited agent deletion to fail")
	}
}
