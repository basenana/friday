package filetools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
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

	wantOrder := []string{"pkg/service/CLAUDE.md", "pkg/AGENTS.md"}
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
	if strings.Contains(result.FYI, "root rules") {
		t.Fatalf("project-root instructions must not be returned as FYI: %q", result.FYI)
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

func TestInstructionSelectionChangeIsDetectedWithoutInvalidation(t *testing.T) {
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
	mustWrite(t, filepath.Join(root, "pkg", "AGENTS.md"), "rules")
	mustWrite(t, filepath.Join(root, "pkg", "main.go"), "package main")
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
			results <- callTool(t, read, sess, map[string]any{"path": "pkg/main.go"})
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

func TestMutationWaitsForNewNestedInstructions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pkg", "AGENTS.md"), "do not guess")
	path := filepath.Join(root, "pkg", "main.go")
	mustWrite(t, path, "old")
	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)
	edit := findTool(t, hook.Tools(), sandbox.FsEditToolName)
	req := &tools.Request{Arguments: map[string]any{
		"path": "pkg/main.go", "old_text": "old", "new_text": "new",
	}, SessionID: sess.ID, SessionRecords: sess}

	first, err := edit.Handler(context.Background(), req)
	if err != nil || first == nil || !first.IsError || first.ErrorCode != "project_instructions_required" || !strings.Contains(first.FYI, "do not guess") {
		t.Fatalf("first mutation result=%#v err=%v", first, err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "old" {
		t.Fatalf("mutation ran before instructions: content=%q err=%v", content, err)
	}
	second, err := edit.Handler(context.Background(), req)
	if err != nil || second == nil || second.IsError {
		t.Fatalf("second mutation result=%#v err=%v", second, err)
	}
	content, err = os.ReadFile(path)
	if err != nil || string(content) != "new" {
		t.Fatalf("retry did not edit file: content=%q err=%v", content, err)
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
	if got := len([]rune(truncateWithNotice(strings.Repeat("界", maxFYIRunes+1), maxFYIRunes))); got != maxFYIRunes {
		t.Fatalf("FYI runes = %d", got)
	}
	if !strings.Contains(limited, "truncated by Friday") {
		t.Fatalf("line truncation notice missing: %q", limited)
	}
}

func TestBeforeModelIncludesWorkspaceWithoutProjectInstructions(t *testing.T) {
	root := t.TempDir()
	hook := newTestHook(t, root)
	req := providers.NewRequest("", types.Message{Role: types.RoleUser, Content: "hello"})

	if err := hook.BeforeModel(context.Background(), session.New("session-1", nil), req); err != nil {
		t.Fatal(err)
	}
	history := req.History()
	if len(history) != 2 || history[0].Role != types.RoleAgent || history[1].Content != "hello" {
		t.Fatalf("history = %#v", history)
	}
	content := history[0].Content
	if !strings.Contains(content, `"`+hook.root+`"`) ||
		!strings.Contains(content, "current project directory and default scope") ||
		!strings.Contains(content, "Do not inspect or modify files outside") ||
		!strings.Contains(content, "Use project-relative paths as presented by filesystem tools") ||
		!strings.Contains(content, "keep that access minimal") {
		t.Fatalf("workspace bootstrap = %q", content)
	}
	if strings.Contains(content, "Contents of ") || strings.Contains(content, "AGENTS.md") {
		t.Fatalf("empty project rendered fake instructions: %q", content)
	}
}

func TestBeforeModelAppendsCodingContractOnce(t *testing.T) {
	root := t.TempDir()
	hook := newTestHook(t, root)

	for i := 0; i < 2; i++ {
		req := providers.NewRequest("sentinel base prompt")
		if err := hook.BeforeModel(context.Background(), session.New("session-1", nil), req); err != nil {
			t.Fatal(err)
		}
		systemPrompt := req.SystemPrompt()
		baseIndex := strings.Index(systemPrompt, "sentinel base prompt")
		contractIndex := strings.Index(systemPrompt, "Preserve pre-existing and unrelated user changes")
		if baseIndex < 0 || contractIndex <= baseIndex {
			t.Fatalf("system prompt ordering = %q", systemPrompt)
		}
		for _, invariant := range []string{
			"existing architecture, naming, style, utilities, and dependencies",
			"destructive Git commands",
			"Never claim completion without actual evidence",
		} {
			if !strings.Contains(systemPrompt, invariant) {
				t.Fatalf("coding contract missing %q: %q", invariant, systemPrompt)
			}
		}
		if strings.Count(systemPrompt, "Preserve pre-existing and unrelated user changes") != 1 {
			t.Fatalf("coding contract count != 1: %q", systemPrompt)
		}
		if strings.Contains(req.History()[0].Content, "Project coding baseline:") {
			t.Fatalf("coding contract leaked into bootstrap history: %#v", req.History())
		}
	}
}

func TestReservedTokensIncludesWorkspaceWithoutProjectInstructions(t *testing.T) {
	root := t.TempDir()
	hook := newTestHook(t, root)
	content := hook.projectInstructions(context.Background())
	want := session.EstimateHistoryTokens([]types.Message{{Role: types.RoleAgent, Content: content}})
	if got := hook.ReservedTokens(nil); got <= 0 || got != want {
		t.Fatalf("ReservedTokens() = %d, want %d", got, want)
	}
}

func TestBeforeModelMaintainsPersistentProjectInstructions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root agents")
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "root claude")
	mustWrite(t, filepath.Join(root, ".friday", "CLAUDE.md"), "friday claude")
	hook := newTestHook(t, root)
	req := providers.NewRequest("", types.Message{Role: types.RoleAgent, Content: "memory"})

	if err := hook.BeforeModel(context.Background(), session.New("session-1", nil), req); err != nil {
		t.Fatal(err)
	}
	history := req.History()
	if len(history) != 2 || history[0].Role != types.RoleAgent || history[1].Content != "memory" {
		t.Fatalf("history = %#v", history)
	}
	content := history[0].Content
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaceIndex := strings.Index(content, "# workspace")
	rootIndex := strings.Index(content, "Contents of "+filepath.Join(canonicalRoot, "AGENTS.md"))
	fridayIndex := strings.Index(content, "Contents of "+filepath.Join(canonicalRoot, ".friday", "CLAUDE.md"))
	if workspaceIndex < 0 || rootIndex <= workspaceIndex || fridayIndex <= rootIndex || !strings.Contains(content, "root agents") || !strings.Contains(content, "friday claude") {
		t.Fatalf("persistent instructions = %q", content)
	}
	if strings.Contains(content, "root claude") {
		t.Fatalf("lower-priority root CLAUDE.md was included: %q", content)
	}
}

func TestBeforeModelRefreshesHumanEdits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	mustWrite(t, path, "old rules")
	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)

	first := providers.NewRequest("")
	if err := hook.BeforeModel(context.Background(), sess, first); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, "new rules")
	second := providers.NewRequest("")
	if err := hook.BeforeModel(context.Background(), sess, second); err != nil {
		t.Fatal(err)
	}
	if got := second.History()[0].Content; !strings.Contains(got, "new rules") || strings.Contains(got, "old rules") || strings.Count(got, "# workspace") != 1 {
		t.Fatalf("refreshed instructions = %q", got)
	}
}

func TestNestedInstructionMTimeChangeIsReinjected(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pkg", "AGENTS.md")
	mustWrite(t, path, "old rules")
	mustWrite(t, filepath.Join(root, "pkg", "main.go"), "package pkg")
	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)

	_ = callTool(t, read, sess, map[string]any{"path": "pkg/main.go"})
	mustWrite(t, path, "new rules")
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, read, sess, map[string]any{"path": "pkg/main.go"})
	if !strings.Contains(result.FYI, "new rules") {
		t.Fatalf("modified instructions not reinjected: %q", result.FYI)
	}
}

func TestNestedInstructionDeletionFallsBackToClaude(t *testing.T) {
	root := t.TempDir()
	agentsPath := filepath.Join(root, "pkg", "AGENTS.md")
	mustWrite(t, agentsPath, "agents rules")
	mustWrite(t, filepath.Join(root, "pkg", "CLAUDE.md"), "claude rules")
	mustWrite(t, filepath.Join(root, "pkg", "main.go"), "package pkg")
	hook := newTestHook(t, root)
	sess := session.New("session-1", nil)
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)

	_ = callTool(t, read, sess, map[string]any{"path": "pkg/main.go"})
	if err := os.Remove(agentsPath); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, read, sess, map[string]any{"path": "pkg/main.go"})
	if !strings.Contains(result.FYI, "## pkg/CLAUDE.md") || !strings.Contains(result.FYI, "claude rules") {
		t.Fatalf("fallback instructions not reinjected: %q", result.FYI)
	}
}

func TestFridayDirectoryInstructionsAreExcludedFromFYI(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".friday", "AGENTS.md"), "persistent rules")
	mustWrite(t, filepath.Join(root, ".friday", "nested", "CLAUDE.md"), "nested rules")
	mustWrite(t, filepath.Join(root, ".friday", "nested", "main.go"), "package nested")
	hook := newTestHook(t, root)
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
	result := callTool(t, read, session.New("session-1", nil), map[string]any{"path": ".friday/nested/main.go"})

	if !strings.Contains(result.FYI, "## .friday/nested/CLAUDE.md") || strings.Contains(result.FYI, "persistent rules") {
		t.Fatalf("unexpected .friday FYI: %q", result.FYI)
	}
}

func TestHookDiscoversInstructionsThroughWorkspaceSymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "AGENTS.md"), "linked root rules")
	mustWrite(t, filepath.Join(target, "pkg", "CLAUDE.md"), "linked package rules")
	mustWrite(t, filepath.Join(target, "pkg", "main.go"), "package pkg")
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Filesystem.ReadOnly = append(cfg.Sandbox.Filesystem.ReadOnly, target)
	hook, err := New(sandbox.NewExecutor(cfg), root)
	if err != nil {
		t.Fatal(err)
	}
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
	result := callTool(t, read, session.New("session-1", nil), map[string]any{"path": "linked/pkg/main.go"})

	packageIndex := strings.Index(result.FYI, "## linked/pkg/CLAUDE.md")
	rootIndex := strings.Index(result.FYI, "## linked/AGENTS.md")
	if packageIndex < 0 || rootIndex <= packageIndex || !strings.Contains(result.FYI, "linked package rules") || !strings.Contains(result.FYI, "linked root rules") {
		t.Fatalf("linked instructions = %q", result.FYI)
	}
	if strings.Contains(result.FYI, filepath.ToSlash(target)) {
		t.Fatalf("physical symlink target leaked: %q", result.FYI)
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
	req := providers.NewRequest("")
	if err := hook.BeforeModel(context.Background(), session.New("session-1", nil), req); err != nil {
		t.Fatal(err)
	}
	history := req.History()
	if len(history) != 1 || !strings.Contains(history[0].Content, "# workspace") || strings.Contains(history[0].Content, "outside rules") {
		t.Fatalf("outside instruction was loaded: %#v", history)
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
	mustWrite(t, filepath.Join(root, "pkg", "AGENTS.md"), "rules")
	mustWrite(t, filepath.Join(root, "pkg", "main.go"), "package main")
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	exec := sandbox.NewExecutor(cfg)
	custom := &countingFileSystem{FileSystem: sandbox.NewLocalFileSystem(exec, root)}
	hook, err := New(nil, root, WithFileSystem(custom))
	if err != nil {
		t.Fatal(err)
	}
	read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
	_ = callTool(t, read, session.New("session-1", nil), map[string]any{"path": "pkg/main.go"})
	if custom.reads == 0 {
		t.Fatal("injected filesystem was not used")
	}
}

func TestPersistedRecordHydratesBeforeFork(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "pkg", "AGENTS.md"), "rules")
	mustWrite(t, filepath.Join(root, "pkg", "main.go"), "package main")
	store := sessionfile.NewFileSessionStore(t.TempDir())
	first, err := store.Create("session-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstHook := newTestHook(t, root)
	read := findTool(t, firstHook.Tools(), sandbox.FsReadToolName)
	if result := callTool(t, read, first, map[string]any{"path": "pkg/main.go"}); result.FYI == "" {
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
	if result := callTool(t, forkRead, fork, map[string]any{"path": "pkg/main.go"}); result.FYI != "" {
		t.Fatalf("fork repeated persisted FYI: %q", result.FYI)
	}
}

func TestLegacyAndMalformedRecordsAreOverwrittenByNewStampShape(t *testing.T) {
	for name, old := range map[string][]byte{
		"legacy":    []byte(`{"version":1,"directories":["pkg"]}`),
		"malformed": []byte(`not-json`),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "pkg", "AGENTS.md"), "rules")
			mustWrite(t, filepath.Join(root, "pkg", "main.go"), "package pkg")
			hook := newTestHook(t, root)
			sess := session.New("session-1", nil)
			if err := sess.UpdateRecord(context.Background(), hook.namespace, func([]byte) ([]byte, error) { return old, nil }); err != nil {
				t.Fatal(err)
			}

			read := findTool(t, hook.Tools(), sandbox.FsReadToolName)
			if result := callTool(t, read, sess, map[string]any{"path": "pkg/main.go"}); result.FYI == "" {
				t.Fatal("old record suppressed FYI")
			}
			stored, err := sess.ReadRecord(context.Background(), hook.namespace)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(stored, []byte("version")) || bytes.Contains(stored, []byte("directories")) {
				t.Fatalf("old shape retained: %s", stored)
			}
			var record instructionRecord
			if err := json.Unmarshal(stored, &record); err != nil || record["pkg"].SelectedFile != "AGENTS.md" {
				t.Fatalf("new stamp record = %s, err=%v", stored, err)
			}
		})
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
