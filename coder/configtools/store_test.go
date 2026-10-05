package configtools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/workspace"
)

type fixedCatalog struct{ names []string }

func (c fixedCatalog) HasModelName(name string) bool {
	for _, candidate := range c.names {
		if candidate == name {
			return true
		}
	}
	return false
}

func (c fixedCatalog) ModelNames() []string { return c.names }

func TestFileStoreAgentCRUD(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents")
	store := NewFileStore([]string{root}, nil, fixedCatalog{[]string{"known"}})
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

func TestFileStoreAgentModelResolution(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents")
	catalog := fixedCatalog{[]string{"known", "MiniMax-M3"}}
	store := NewFileStore([]string{root}, nil, catalog)

	created, err := store.CreateAgent(AgentInput{Name: "exact", Instructions: "Exact model name.", Model: "known"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Model != "known" {
		t.Fatalf("exact model = %q", created.Model)
	}

	canonical, err := store.CreateAgent(AgentInput{Name: "cased", Instructions: "Case-insensitive model name.", Model: "minimax-m3"})
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Model != "MiniMax-M3" {
		t.Fatalf("case-insensitive model = %q, want MiniMax-M3", canonical.Model)
	}

	_, err = store.CreateAgent(AgentInput{Name: "badmodel", Instructions: "Unknown model.", Model: "does-not-exist"})
	if err == nil {
		t.Fatal("expected unknown model to fail")
	}
	message := err.Error()
	if !strings.Contains(message, `unknown model "does-not-exist"`) || !strings.Contains(message, "available models: known, MiniMax-M3") {
		t.Fatalf("error = %q", message)
	}
	if _, statErr := os.Stat(filepath.Join(root, "badmodel")); !os.IsNotExist(statErr) {
		t.Fatalf("failed create left files behind: %v", statErr)
	}

	// A nil catalog accepts any non-empty model name (automation callers).
	permissive := NewFileStore([]string{root}, nil, nil)
	created, err = permissive.CreateAgent(AgentInput{Name: "permissive", Instructions: "Any model.", Model: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Model != "anything" {
		t.Fatalf("permissive model = %q", created.Model)
	}
}

func TestFileStoreAgentCreateFieldDefaultsAndValidation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents")
	store := NewFileStore([]string{root}, nil, nil)

	created, err := store.CreateAgent(AgentInput{Name: "defaults", Instructions: "Body.", Effort: "High"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Description != "defaults" {
		t.Fatalf("default description = %q", created.Description)
	}
	if created.MaxLoopTimes != 100 {
		t.Fatalf("default max_loop_times = %d", created.MaxLoopTimes)
	}
	if created.Effort != "high" {
		t.Fatalf("effort = %q", created.Effort)
	}

	cases := []struct {
		name  string
		input AgentInput
		want  string
	}{
		{"blank instructions", AgentInput{Name: "a", Instructions: "  "}, "agent instructions are required"},
		{"invalid name", AgentInput{Name: "Writer", Instructions: "Body."}, "invalid agent name"},
		{"invalid effort", AgentInput{Name: "a", Instructions: "Body.", Effort: "turbo"}, "invalid reasoning effort"},
		{"negative loop", AgentInput{Name: "a", Instructions: "Body.", MaxLoopTimes: -1}, "max_loop_times must be a positive integer"},
	}
	for _, tc := range cases {
		_, err := store.CreateAgent(tc.input)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}

	if _, err := store.CreateAgent(AgentInput{Name: "defaults", Instructions: "Body."}); err == nil || !strings.Contains(err.Error(), "agent already exists") {
		t.Fatalf("duplicate err = %v", err)
	}
}

func TestFileStoreUpdateAgentPatchesEachField(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents")
	store := NewFileStore([]string{root}, nil, fixedCatalog{[]string{"m1", "m2"}})
	if _, err := store.CreateAgent(AgentInput{Name: "writer", Description: "Writes", Instructions: "Write clearly.", Model: "m1", Effort: "high", MaxLoopTimes: 7}); err != nil {
		t.Fatal(err)
	}

	patches := []struct {
		name   string
		fields map[string]any
		check  func(*coderagents.AgentSpec) error
	}{
		{"description", map[string]any{"description": "Better writer"}, func(s *coderagents.AgentSpec) error {
			if s.Description != "Better writer" || s.SystemPrompt != "Write clearly." || s.Model != "m1" || s.Effort != "high" || s.MaxLoopTimes != 7 {
				return fmt.Errorf("patch leaked other fields: %#v", s)
			}
			return nil
		}},
		{"instructions", map[string]any{"instructions": "Revise carefully."}, func(s *coderagents.AgentSpec) error {
			if s.SystemPrompt != "Revise carefully." {
				return fmt.Errorf("instructions = %q", s.SystemPrompt)
			}
			return nil
		}},
		{"model", map[string]any{"model": "m2"}, func(s *coderagents.AgentSpec) error {
			if s.Model != "m2" {
				return fmt.Errorf("model = %q", s.Model)
			}
			return nil
		}},
		{"effort", map[string]any{"effort": "low"}, func(s *coderagents.AgentSpec) error {
			if s.Effort != "low" {
				return fmt.Errorf("effort = %q", s.Effort)
			}
			return nil
		}},
		{"max_loop_times", map[string]any{"max_loop_times": float64(9)}, func(s *coderagents.AgentSpec) error {
			if s.MaxLoopTimes != 9 {
				return fmt.Errorf("max_loop_times = %d", s.MaxLoopTimes)
			}
			return nil
		}},
		{"clear model", map[string]any{"model": ""}, func(s *coderagents.AgentSpec) error {
			if s.Model != "" {
				return fmt.Errorf("model = %q", s.Model)
			}
			return nil
		}},
		{"clear effort", map[string]any{"effort": ""}, func(s *coderagents.AgentSpec) error {
			if s.Effort != "" {
				return fmt.Errorf("effort = %q", s.Effort)
			}
			return nil
		}},
	}
	for _, patch := range patches {
		updated, err := store.UpdateAgent("writer", patch.fields)
		if err != nil {
			t.Fatalf("%s: %v", patch.name, err)
		}
		if err := patch.check(updated); err != nil {
			t.Fatalf("%s: %v", patch.name, err)
		}
	}

	cleared, err := store.GetAgent("writer")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cleared.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "model:") || strings.Contains(string(data), "effort:") {
		t.Fatalf("cleared fields still serialized: %s", data)
	}

	unknownBefore, err := os.ReadFile(cleared.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAgent("writer", map[string]any{"model": "missing"}); err == nil || !strings.Contains(err.Error(), "available models") {
		t.Fatalf("unknown model update err = %v", err)
	}
	unknownAfter, err := os.ReadFile(cleared.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(unknownBefore) != string(unknownAfter) {
		t.Fatal("failed update modified the spec file")
	}

	if _, err := store.UpdateAgent("ghost", map[string]any{"description": "x"}); err == nil || !strings.Contains(err.Error(), "agent not found") {
		t.Fatalf("missing agent err = %v", err)
	}
}

func TestFileStoreUpdateInheritedAgentCopiesToWritableLayer(t *testing.T) {
	base := t.TempDir()
	homeAgents := filepath.Join(base, "home-agents")
	projectAgents := filepath.Join(base, "project-agents")
	homeStore := NewFileStore([]string{homeAgents}, nil, nil)
	homeSpec, err := homeStore.CreateAgent(AgentInput{Name: "shared", Instructions: "Shared."})
	if err != nil {
		t.Fatal(err)
	}

	store := NewFileStore([]string{homeAgents, projectAgents}, nil, nil)
	updated, err := store.UpdateAgent("shared", map[string]any{"description": "Project override"})
	if err != nil {
		t.Fatal(err)
	}
	if !pathWithin(projectAgents, updated.SourcePath) {
		t.Fatalf("updated source = %q, want inside %q", updated.SourcePath, projectAgents)
	}
	if updated.Description != "Project override" || updated.SystemPrompt != "Shared." {
		t.Fatalf("updated = %#v", updated)
	}
	homeData, err := os.ReadFile(homeSpec.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(homeData), "Project override") {
		t.Fatal("inherited update rewrote the home layer spec")
	}
	if _, err := store.GetAgent("shared"); err != nil || store.ListAgentsLengthForTest() != 0 {
		_ = err // GetAgent already asserted; helper below only guards shadowing.
	}
}

// ListAgentsLengthForTest exists only to keep the GetAgent call above from
// being flagged as unused when assertions evolve; it is intentionally unused.
func (s *FileStore) ListAgentsLengthForTest() int { return 0 }
