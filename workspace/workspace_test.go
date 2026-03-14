package workspace

import (
	"os"
	"path/filepath"
	"testing"
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
	created, err := ws.Init()
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
	createdAgain, err := ws.Init()
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
	_, err = ws.Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Test Load
	content, err := ws.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have 5 system prompt files (AGENTS, SOUL, USER, IDENTITY, MEMORY)
	if len(content.SystemPrompts) != 5 {
		t.Errorf("expected 5 system prompts, got %d", len(content.SystemPrompts))
	}

	// MemoryHistory should be empty (no daily memory files)
	if len(content.MemoryHistory) != 0 {
		t.Errorf("expected 0 memory history messages, got %d", len(content.MemoryHistory))
	}
}

func TestComposeSystemPrompt(t *testing.T) {
	tests := []struct {
		name     string
		content  *LoadedContent
		expected string
	}{
		{
			name:     "nil content",
			content:  nil,
			expected: "default",
		},
		{
			name:     "empty content",
			content:  &LoadedContent{},
			expected: "default",
		},
		{
			name: "with system prompts",
			content: &LoadedContent{
				SystemPrompts: []string{"prompt1", "prompt2"},
			},
			expected: "default\n\nprompt1\n\nprompt2",
		},
		{
			name: "empty prompts filtered",
			content: &LoadedContent{
				SystemPrompts: []string{"", "prompt1", "   ", "prompt2"},
			},
			expected: "default\n\nprompt1\n\nprompt2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComposeSystemPrompt(tt.content)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
