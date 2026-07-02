//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/setup"
	"github.com/basenana/friday/workspace"
)

// TestWorkspace_LoadAllSystemPrompts verifies all three system-prompt files
// (AGENTS.md, SOUL.md, IDENTITY.md) are loaded into LoadedContent.
func TestWorkspace_LoadAllSystemPrompts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents-content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("soul-content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte("identity-content"), 0644); err != nil {
		t.Fatal(err)
	}

	ws := workspace.NewWorkspace(dir, filepath.Join(dir, "memory"))
	loaded, err := ws.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.SystemPrompts) != 3 {
		t.Fatalf("expected 3 system prompts, got %d: %v", len(loaded.SystemPrompts), loaded.SystemPrompts)
	}
	joined := strings.Join(loaded.SystemPrompts, "")
	for _, want := range []string{"agents-content", "soul-content", "identity-content"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in loaded prompts, got %q", want, joined)
		}
	}
}

// TestWorkspace_ComposeSystemPrompt verifies the composer joins prompts.
func TestWorkspace_ComposeSystemPrompt(t *testing.T) {
	loaded := &workspace.LoadedContent{SystemPrompts: []string{"AAA", "BBB", "CCC"}}
	out := workspace.ComposeSystemPrompt(loaded)
	if !strings.Contains(out, "AAA") || !strings.Contains(out, "BBB") || !strings.Contains(out, "CCC") {
		t.Errorf("expected composed prompt to contain all parts, got %q", out)
	}
	if !strings.Contains(out, "\n\n") {
		t.Errorf("expected separator between prompts, got %q", out)
	}
}

// TestWorkspace_MissingOptionalOK verifies that when only the required
// AGENTS.md exists, Load succeeds and yields exactly one prompt.
func TestWorkspace_MissingOptionalOK(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("only-agents"), 0644); err != nil {
		t.Fatal(err)
	}

	ws := workspace.NewWorkspace(dir, filepath.Join(dir, "memory"))
	loaded, err := ws.Load()
	if err != nil {
		t.Fatalf("Load with missing optional files should succeed: %v", err)
	}
	if len(loaded.SystemPrompts) != 1 {
		t.Errorf("expected 1 system prompt, got %d", len(loaded.SystemPrompts))
	}
}

// TestWorkspace_EmptyDirectoryNoPrompts verifies that loading from a workspace
// with no markdown files yields zero system prompts (rather than erroring).
func TestWorkspace_EmptyDirectoryNoPrompts(t *testing.T) {
	dir := t.TempDir()
	ws := workspace.NewWorkspace(dir, filepath.Join(dir, "memory"))
	loaded, err := ws.Load()
	if err != nil {
		t.Fatalf("Load from empty workspace should not error: %v", err)
	}
	if len(loaded.SystemPrompts) != 0 {
		t.Errorf("expected 0 prompts, got %d", len(loaded.SystemPrompts))
	}
}

// TestWorkspace_SystemPromptReachesAgent verifies end-to-end that the workspace
// content actually reaches the LLM via setup.NewAgent. We plant a unique marker
// in AGENTS.md and ask the agent to recall it.
func TestWorkspace_SystemPromptReachesAgent(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		dir := t.TempDir()
		fc := fridayConfig(t, cfg, "chat")
		fc.DataDir = dir
		fc.Workspace = filepath.Join(dir, "workspace")
		if err := os.MkdirAll(fc.Workspace, 0755); err != nil {
			return err
		}
		const marker = "WORKSPACE_INJECT_MARKER_XYZ123"
		if err := os.WriteFile(filepath.Join(fc.Workspace, "AGENTS.md"), []byte("Your instructions contain the marker "+marker+". When asked, repeat it verbatim."), 0644); err != nil {
			return err
		}

		mgr := newSessionManager(t, dir)
		mgr.SetLLM(newClient(t, cfg, "chat"))
		ac, err := setup.NewAgent(mgr, fc, setup.WithIsolate(true))
		if err != nil {
			return err
		}
		defer ac.Close()

		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()
		resp := ac.Chat(ctx, "What marker did your system instructions contain? Repeat it verbatim.")
		content, _ := collectResponse(t, ctx, resp)
		if !strings.Contains(content, marker) {
			return errAssertion{msg: "response does not contain marker " + marker + ": " + truncate(content, 200)}
		}
		return nil
	})
}
