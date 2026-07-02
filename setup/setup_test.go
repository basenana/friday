package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/api"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
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

func TestNewAgentDoesNotInjectWorkspaceMemoryIntoNewSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "setup-no-workspace-memory-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(tmpDir, "data")
	cfg.Workspace = filepath.Join(tmpDir, "workspace")
	cfg.Model.Provider = "openai"
	cfg.Model.Model = "test-model"

	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("init workspace failed: %v", err)
	}
	if err := ws.Write("MEMORY.md", "remember persistent workspace facts"); err != nil {
		t.Fatalf("write MEMORY.md failed: %v", err)
	}
	if err := os.MkdirAll(cfg.MemoryPath(), 0755); err != nil {
		t.Fatalf("create memory dir failed: %v", err)
	}
	memLogPath := filepath.Join(cfg.MemoryPath(), time.Now().Format("2006-01-02")+".md")
	if err := os.WriteFile(memLogPath, []byte("daily memory note"), 0644); err != nil {
		t.Fatalf("write memory log failed: %v", err)
	}

	sessionStore := file.NewFileSessionStore(cfg.SessionsPath())
	sessionMgr := sessions.NewManager(sessionStore, filepath.Join(cfg.DataDirPath(), "current"), "")

	agentCtx, err := NewAgent(sessionMgr, cfg)
	if err != nil {
		t.Fatalf("NewAgent failed: %v", err)
	}

	if len(agentCtx.Session.GetHistory()) != 0 {
		t.Fatalf("expected new session history to stay empty; got %#v", agentCtx.Session.GetHistory())
	}
}

type hookToolAppender struct{}

func (h *hookToolAppender) BeforeAgent(ctx context.Context, sess *coresession.Session, req coresession.AgentRequest) error {
	req.AppendTools(tools.NewTool("managed_tool", tools.WithDescription("managed test tool")))
	return nil
}

func TestGetOrCreateManagedSessionInstallsHooks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "setup-managed-session-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := file.NewFileSessionStore(filepath.Join(tmpDir, "sessions"))
	sessionMgr := sessions.NewManager(store, filepath.Join(tmpDir, "current"), "")

	sess, _, err := getOrCreateManagedSession(sessionMgr, "proposal-1", []coresession.Hook{&hookToolAppender{}})
	if err != nil {
		t.Fatalf("getOrCreateManagedSession failed: %v", err)
	}

	req := &api.Request{}
	if err := sess.RunHooks(context.Background(), types.SessionHookBeforeAgent, coresession.HookPayload{AgentRequest: req}); err != nil {
		t.Fatalf("RunHooks failed: %v", err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "managed_tool" {
		t.Fatalf("expected managed hook tool to be installed, got %#v", req.Tools)
	}

	currentID, err := sessionMgr.GetCurrentID()
	if err != nil {
		t.Fatalf("GetCurrentID failed: %v", err)
	}
	if currentID != "" {
		t.Fatalf("managed session should not become current, got %q", currentID)
	}
}
