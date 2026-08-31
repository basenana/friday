package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewWorkspace(t *testing.T) {
	ws := NewWorkspace("/tmp/workspace", "/tmp/memory")
	if ws.BasePath() != "/tmp/workspace" {
		t.Errorf("expected basePath /tmp/workspace, got %s", ws.BasePath())
	}
	if ws.MemoryPath() != "/tmp/memory" {
		t.Errorf("expected memoryPath /tmp/memory, got %s", ws.MemoryPath())
	}
}

func TestWorkspaceInit(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")

	ws := NewWorkspace(workspacePath, memoryPath)

	// Test Init
	created, err := ws.InitWithParams(nil)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if len(created) != len(DefaultContents) {
		t.Errorf("expected %d created files, got %d", len(DefaultContents), len(created))
	}

	// Verify directories exist
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		t.Error("workspace directory not created")
	}
	if _, err := os.Stat(memoryPath); os.IsNotExist(err) {
		t.Error("memory directory not created")
	}

	// Verify files exist
	for filename := range DefaultContents {
		filePath := filepath.Join(workspacePath, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Errorf("file %s not created", filename)
		}
	}

	// Test Init again - should return empty created list
	createdAgain, err := ws.InitWithParams(nil)
	if err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
	if len(createdAgain) != 0 {
		t.Errorf("expected 0 created files on second init, got %d", len(createdAgain))
	}
}

func TestWorkspaceLoad(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")

	ws := NewWorkspace(workspacePath, memoryPath)

	// Init workspace
	_, err = ws.InitWithParams(nil)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Test Load
	content, err := ws.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have 3 system prompt files (AGENTS, SOUL, IDENTITY)
	if len(content.SystemPrompts) != 3 {
		t.Errorf("expected 3 system prompts, got %d", len(content.SystemPrompts))
	}

	// MEMORY.md has FileRoleMemory, so it should not be in SystemPrompts
	if len(content.SystemPrompts) != 3 {
		t.Errorf("expected memory files to stay out of loaded content prompts, got %d system prompts", len(content.SystemPrompts))
	}

	// Fresh workspace: MEMORY.md only contains the empty template header,
	// so no memory history messages should be produced.
	if len(content.MemoryHistory) != 0 {
		t.Errorf("expected 0 memory history messages for fresh workspace, got %d", len(content.MemoryHistory))
	}
}

func TestWorkspaceLoadIncludesLongTermAndRecentMemory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "workspace-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")

	ws := NewWorkspace(workspacePath, memoryPath)
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(workspacePath, "MEMORY.md"), []byte("# MEMORY.md\n\n- Prefers concise answers"), 0644); err != nil {
		t.Fatalf("failed to write MEMORY.md: %v", err)
	}
	today := time.Now().Format("2006-01-02")
	if err := os.WriteFile(filepath.Join(memoryPath, today+".md"), []byte("Investigated low mapping rate in sample T1."), 0644); err != nil {
		t.Fatalf("failed to write daily memory: %v", err)
	}

	content, err := ws.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(content.MemoryHistory) != 2 {
		t.Fatalf("expected 2 memory history messages, got %d", len(content.MemoryHistory))
	}
	if !strings.Contains(content.MemoryHistory[0].Content, "[Long-Term Memory]") {
		t.Fatalf("expected long-term memory header, got %q", content.MemoryHistory[0].Content)
	}
	if !strings.Contains(content.MemoryHistory[1].Content, "[Recent Memory Context]") {
		t.Fatalf("expected recent memory header, got %q", content.MemoryHistory[1].Content)
	}
}

func TestWorkspaceLoadMemoryDaysOption(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "workspace-memory-days-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	workspacePath := filepath.Join(tmpDir, "workspace")
	memoryPath := filepath.Join(tmpDir, "memory")
	ws := NewWorkspace(workspacePath, memoryPath)
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Write one daily log three days back and one for today.
	oldDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	for name, content := range map[string]string{
		oldDate + ".md": "Older than the default window.",
		today + ".md":   "Today's note.",
	} {
		if err := os.WriteFile(filepath.Join(memoryPath, name), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	// Default: only today + yesterday.
	defaultContent, err := ws.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(defaultContent.MemoryHistory[len(defaultContent.MemoryHistory)-1].Content, "Today's note.") {
		t.Fatalf("expected today's log by default, got %#v", defaultContent.MemoryHistory)
	}
	if strings.Contains(defaultContent.MemoryHistory[len(defaultContent.MemoryHistory)-1].Content, "Older than the default window.") {
		t.Fatalf("3-day-old log should not load with the default 2-day window, got %#v", defaultContent.MemoryHistory)
	}

	// Configured retention: 14 days pulls in the 3-day-old log.
	content, err := ws.Load(WithMemoryDays(14))
	if err != nil {
		t.Fatalf("Load(WithMemoryDays(14)) failed: %v", err)
	}
	combined := content.MemoryHistory[len(content.MemoryHistory)-1].Content
	if !strings.Contains(combined, "Older than the default window.") || !strings.Contains(combined, "Today's note.") {
		t.Fatalf("expected both logs with 14-day window, got %#v", content.MemoryHistory)
	}

	// Invalid values fall back to the default.
	fallback, err := ws.Load(WithMemoryDays(0))
	if err != nil {
		t.Fatalf("Load(WithMemoryDays(0)) failed: %v", err)
	}
	if len(fallback.MemoryHistory) != len(defaultContent.MemoryHistory) {
		t.Fatalf("expected zero value to fall back to default, got %#v", fallback.MemoryHistory)
	}
}

func TestComposeSystemPrompt(t *testing.T) {
	tests := []struct {
		name        string
		content     *LoadedContent
		contains    string
		notContains string
	}{
		{
			name: "empty prompts filtered",
			content: &LoadedContent{
				SystemPrompts: []string{"", "prompt1", "   ", "prompt2"},
			},
			contains: "prompt1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComposeSystemPrompt(tt.content)
			if tt.contains != "" && !strings.Contains(result, tt.contains) {
				t.Errorf("expected result to contain %q, got %q", tt.contains, result)
			}
			if tt.notContains != "" && strings.Contains(result, tt.notContains) {
				t.Errorf("expected result not to contain %q, got %q", tt.notContains, result)
			}
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	tests := []struct {
		name     string
		tmpl     string
		params   *TemplateParams
		expected string
	}{
		{
			name:     "nil params uses defaults",
			tmpl:     "DataDir: {{if .Paths}}{{.Paths.DataDir}}{{else}}~/.friday{{end}}",
			params:   nil,
			expected: "DataDir: ~/.friday",
		},
		{
			name: "with paths renders correctly",
			tmpl: "DataDir: {{.Paths.DataDir}}",
			params: &TemplateParams{
				Paths: &Paths{
					DataDir: "/custom/data",
				},
			},
			expected: "DataDir: /custom/data",
		},
		{
			name:     "plain text unchanged",
			tmpl:     "Hello World",
			params:   nil,
			expected: "Hello World",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RenderTemplate(tt.tmpl, tt.params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
