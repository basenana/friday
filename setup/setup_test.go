package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/workspace"
)

func TestAppendImageRefsToMessage(t *testing.T) {
	message := appendImageRefsToMessage("What is in this image?", []string{"/tmp/example.png"})

	if !strings.Contains(message, "/tmp/example.png") {
		t.Fatalf("expected image reference to be included, got %q", message)
	}
	if !strings.Contains(message, "use the image tool") {
		t.Fatalf("expected image tool hint to be included, got %q", message)
	}
}

func TestWorkspaceLoadProvidesSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "setup-system-prompt-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ws := workspace.NewWorkspace(filepath.Join(tmpDir, "workspace"), filepath.Join(tmpDir, "memory"))
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("init workspace failed: %v", err)
	}
	loaded, err := ws.Load()
	if err != nil {
		t.Fatalf("load workspace failed: %v", err)
	}

	agentsPrompt, err := ws.Read("AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS.md failed: %v", err)
	}

	systemPrompt := workspace.ComposeSystemPrompt(loaded)

	if !strings.Contains(systemPrompt, strings.TrimSpace(agentsPrompt)) {
		t.Fatalf("expected composed system prompt to include AGENTS.md, got %q", systemPrompt)
	}
}

func TestSessionMemoryBridgeReturnsSnapshotCopy(t *testing.T) {
	original := []types.Message{{Role: types.RoleAgent, Content: "memory snapshot"}}
	bridge := newSessionMemoryBridge(original)

	got, err := bridge.LoadSessionMemory(context.Background(), nil)
	if err != nil {
		t.Fatalf("LoadSessionMemory failed: %v", err)
	}
	if len(got) != 1 || got[0].Content != "memory snapshot" {
		t.Fatalf("unexpected snapshot: %#v", got)
	}

	got[0].Content = "mutated"
	if original[0].Content != "memory snapshot" {
		t.Fatalf("expected bridge to return a copy, got %#v", original)
	}
}
