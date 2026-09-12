package filetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/sandbox"
	sessionfile "github.com/basenana/friday/sessions/file"
)

func TestHookDiscoversNestedInstructionsOnce(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "root rules")
	mustWrite(t, filepath.Join(root, "pkg", "AGENTS.md"), "package agents")
	mustWrite(t, filepath.Join(root, "pkg", "CLAUDE.md"), "package claude")
	mustWrite(t, filepath.Join(root, "pkg", "service", "CLAUDE.md"), "service rules")
	mustWrite(t, filepath.Join(root, "pkg", "service", "main.go"), "package service")

	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
	result := callTool(t, read, sess, map[string]any{"path": "pkg/service/main.go"})

	wantOrder := []string{"pkg/service/CLAUDE.md", "pkg/AGENTS.md", "CLAUDE.md"}
	last := -1
	for _, part := range wantOrder {
		index := strings.Index(result.FYI, "## "+part)
		if index <= last {
			t.Fatalf("FYI order/content mismatch for %q: %q", part, result.FYI)
		}
		last = index
	}
	if strings.Contains(result.FYI, "package claude") {
		t.Fatalf("CLAUDE.md must not be read when AGENTS.md exists in the same directory: %q", result.FYI)
	}

	second := callTool(t, read, sess, map[string]any{"path": "pkg/service/main.go"})
	if second.FYI != "" {
		t.Fatalf("second FYI = %q, want empty", second.FYI)
	}
}

func TestHookOnlyReturnsNewDirectoryInstructions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root")
	mustWrite(t, filepath.Join(root, "a", "AGENTS.md"), "a")
	mustWrite(t, filepath.Join(root, "a", "one.go"), "one")
	mustWrite(t, filepath.Join(root, "b", "CLAUDE.md"), "b")
	mustWrite(t, filepath.Join(root, "b", "two.go"), "two")

	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
	_ = callTool(t, read, sess, map[string]any{"path": "a/one.go"})
	result := callTool(t, read, sess, map[string]any{"path": "b/two.go"})
	if !strings.Contains(result.FYI, "## b/CLAUDE.md") || strings.Contains(result.FYI, "## AGENTS.md") {
		t.Fatalf("FYI should contain only the unseen sibling directory: %q", result.FYI)
	}
}

func TestInstructionWriteInvalidatesDirectory(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pkg", "CLAUDE.md"), "old")
	mustWrite(t, filepath.Join(root, "pkg", "main.go"), "package pkg")
	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)
	toolList := hook.Tools()
	read := findTool(t, toolList, sandbox.FsReadToolName)
	write := findTool(t, toolList, sandbox.FsWriteToolName)

	_ = callTool(t, read, sess, map[string]any{"path": "pkg/main.go"})
	writeResult := callTool(t, write, sess, map[string]any{"path": "pkg/AGENTS.md", "content": "new"})
	if writeResult.FYI != "" {
		t.Fatalf("write FYI = %q, want empty", writeResult.FYI)
	}
	result := callTool(t, read, sess, map[string]any{"path": "pkg/main.go"})
	if !strings.Contains(result.FYI, "## pkg/AGENTS.md") || !strings.Contains(result.FYI, "new") {
		t.Fatalf("updated instructions not returned: %q", result.FYI)
	}
}

func TestConcurrentReadsClaimDirectoryOnce(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "rules")
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)

	start := make(chan struct{})
	results := make(chan *tools.Result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- callTool(t, read, sess, map[string]any{"path": "main.go"})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	nonEmpty := 0
	for result := range results {
		if result.FYI != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		t.Fatalf("non-empty FYI results = %d, want 1", nonEmpty)
	}
}

func TestInstructionAndFYILimits(t *testing.T) {
	lines := make([]string, 101)
	for i := range lines {
		lines[i] = "line"
	}
	limited := truncateInstruction(strings.Join(lines, "\n"))
	if got := strings.Count(limited, "line"); got != maxInstructionLines {
		t.Fatalf("line count = %d, want %d", got, maxInstructionLines)
	}
	if got := len([]rune(truncateInstruction(strings.Repeat("x", maxInstructionRunes+1)))); got != maxInstructionRunes {
		t.Fatalf("instruction runes = %d", got)
	}
	if got := len([]rune(truncateRunes(strings.Repeat("界", maxFYIRunes+1), maxFYIRunes))); got != maxFYIRunes {
		t.Fatalf("FYI runes = %d", got)
	}
}

func TestInstructionSymlinkCannotEscapeProjectRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "AGENTS.md"), "outside rules")
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	if err := os.Symlink(filepath.Join(outside, "AGENTS.md"), filepath.Join(root, "AGENTS.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Filesystem.ReadOnly = append(cfg.Sandbox.Filesystem.ReadOnly, outside)
	exec := sandbox.NewExecutor(cfg)
	hook, err := New(exec, root)
	if err != nil {
		t.Fatal(err)
	}
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
	result := callTool(t, read, session.New("session-1", nil), map[string]any{"path": "main.go"})
	if result.FYI != "" {
		t.Fatalf("outside instruction was loaded: %q", result.FYI)
	}
}

type countingFileSystem struct {
	sandbox.FileSystem
	reads int
}

func (f *countingFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	f.reads++
	return f.FileSystem.ReadFile(ctx, path)
}

func TestHookUsesInjectedFileSystem(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	exec := sandbox.NewExecutor(cfg)
	custom := &countingFileSystem{FileSystem: sandbox.NewLocalFileSystem(exec, root)}
	hook, err := New(nil, root, WithFileSystem(custom))
	if err != nil {
		t.Fatal(err)
	}
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
	_ = callTool(t, read, session.New("session-1", nil), map[string]any{"path": "main.go"})
	if custom.reads == 0 {
		t.Fatal("injected filesystem was not used")
	}
}

func TestPersistedRecordHydratesBeforeFork(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "rules")
	mustWrite(t, filepath.Join(root, "main.go"), "package main")
	store := sessionfile.NewFileSessionStore(t.TempDir())
	first, err := store.Create("session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstHook := newTestHook(t, root)
	read := findTool(t, firstHook.Tools(), sandbox.FsReadToolName)
	if result := callTool(t, read, first, map[string]any{"path": "main.go"}); result.FYI == "" {
		t.Fatal("initial read did not return FYI")
	}

	reloaded, err := store.Load("session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	secondHook := newTestHook(t, root)
	if err := secondHook.BeforeAgent(context.Background(), reloaded, &api.Request{}); err != nil {
		t.Fatal(err)
	}
	fork := reloaded.Fork()
	forkRead := findTool(t, secondHook.Tools(), sandbox.FsReadToolName)
	if result := callTool(t, forkRead, fork, map[string]any{"path": "main.go"}); result.FYI != "" {
		t.Fatalf("fork repeated persisted FYI: %q", result.FYI)
	}
}

func newTestHook(t *testing.T, root string) *Hook {
	t.Helper()
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	hook, err := New(sandbox.NewExecutor(cfg), root)
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func findTool(t *testing.T, toolList []*tools.Tool, name string) *tools.Tool {
	t.Helper()
	for _, tool := range toolList {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func callTool(t *testing.T, tool *tools.Tool, sess *session.Session, args map[string]any) *tools.Result {
	t.Helper()
	result, err := tool.Handler(context.Background(), &tools.Request{
		Arguments:      args,
		SessionID:      sess.ID,
		SessionRecords: sess,
	})
	if err != nil {
		t.Fatalf("tool %s: %v", tool.Name, err)
	}
	if result == nil || result.IsError {
		t.Fatalf("tool %s result = %#v", tool.Name, result)
	}
	return result
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
