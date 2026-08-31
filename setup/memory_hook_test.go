package setup

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/providers"
	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/workspace"
)

func TestMemoryHookInjectsFreshMemoryPerRequest(t *testing.T) {
	tmpDir := t.TempDir()
	ws := workspace.NewWorkspace(filepath.Join(tmpDir, "workspace"), filepath.Join(tmpDir, "memory"))
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("init workspace failed: %v", err)
	}
	if err := ws.Write("MEMORY.md", "first version of a fact"); err != nil {
		t.Fatalf("write MEMORY.md failed: %v", err)
	}

	hook := newMemoryHook(ws)
	sess := coreSession.New("test-session", nil)

	first := providers.NewRequest("system", types.Message{Role: types.RoleUser, Content: "hi"})
	if err := hook.BeforeModel(context.Background(), sess, first); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}
	if len(first.History()) != 2 { // memory message + user message
		t.Fatalf("expected 2 request messages, got %#v", first.History())
	}
	if !strings.Contains(first.History()[0].Content, "first version of a fact") {
		t.Fatalf("expected memory prepended, got %#v", first.History())
	}
	if first.History()[1].Content != "hi" {
		t.Fatalf("expected original history preserved, got %#v", first.History())
	}
	if len(sess.GetHistory()) != 0 {
		t.Fatalf("session history must not be mutated, got %#v", sess.GetHistory())
	}

	// A long-lived session must see updated memory on the next request.
	if err := ws.Write("MEMORY.md", "updated fact"); err != nil {
		t.Fatalf("write MEMORY.md failed: %v", err)
	}
	second := providers.NewRequest("system", types.Message{Role: types.RoleUser, Content: "again"})
	if err := hook.BeforeModel(context.Background(), sess, second); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}
	if !strings.Contains(second.History()[0].Content, "updated fact") {
		t.Fatalf("expected updated memory, got %#v", second.History())
	}
	if strings.Contains(second.History()[0].Content, "first version of a fact") {
		t.Fatalf("expected stale memory replaced, got %#v", second.History())
	}
}

func TestMemoryHookNoMemoryIsNoop(t *testing.T) {
	tmpDir := t.TempDir()
	ws := workspace.NewWorkspace(filepath.Join(tmpDir, "workspace"), filepath.Join(tmpDir, "memory"))
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatalf("init workspace failed: %v", err)
	}

	hook := newMemoryHook(ws)
	req := providers.NewRequest("system", types.Message{Role: types.RoleUser, Content: "hi"})
	if err := hook.BeforeModel(context.Background(), coreSession.New("s", nil), req); err != nil {
		t.Fatalf("BeforeModel failed: %v", err)
	}
	if len(req.History()) != 1 {
		t.Fatalf("expected request history untouched, got %#v", req.History())
	}
}

func TestToolTraceSinkForConfigDisabledByDefault(t *testing.T) {
	if toolTraceSinkForConfig(nil) != nil {
		t.Fatal("expected nil sink for nil config")
	}
	cfg := config.DefaultConfig()
	if toolTraceSinkForConfig(cfg) != nil {
		t.Fatal("expected nil sink when logging is disabled")
	}
	cfg.Log.Enabled = true
	if toolTraceSinkForConfig(cfg) == nil {
		t.Fatal("expected sink when logging is enabled")
	}
}
