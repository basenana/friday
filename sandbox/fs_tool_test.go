package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/core/tools"
)

func TestFsToolSurfaceIncludesSearchAndOmitsMkdir(t *testing.T) {
	tools := NewFsTools(NewExecutor(DefaultConfig()), t.TempDir())
	got := make(map[string]bool, len(tools))
	for _, tool := range tools {
		got[tool.Name] = true
		if issues := tool.ValidateDefinition(2); len(issues) != 0 {
			t.Fatalf("%s definition: %v", tool.Name, issues)
		}
	}
	for _, name := range []string{"fs_list", "fs_search", "fs_read", "fs_write", "fs_edit", "fs_delete"} {
		if !got[name] {
			t.Fatalf("missing tool %q: %v", name, got)
		}
	}
	if got["fs_mkdir"] {
		t.Fatalf("fs_mkdir must not be model-visible: %v", got)
	}
}

func TestFsListReturnsStructuredMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	result, err := fsListHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{"path": "."}})
	if err != nil || result.IsError {
		t.Fatalf("fs_list result=%+v err=%v", result, err)
	}
	var decoded fsListResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if decoded.Path != "." || len(decoded.Entries) != 2 || decoded.Entries[0].Name != ".hidden" || decoded.Entries[0].Mode == "" {
		t.Fatalf("decoded list = %#v", decoded)
	}
}

func TestFsSearchRecursesAndSkipsGitBinaryAndSymlinks(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"main.go":            "package main\nfunc NewMain() {}\n",
		"vendor/pkg/lib.go":  "func NewVendor() {}\n",
		".hidden/config.txt": "func NewHidden() {}\n",
		".git/ignored.txt":   "func NewIgnored() {}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{'x', 0, 'y'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "main.go"), filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	result, err := fsSearchHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"directory": ".", "regex": `func\s+New`,
	}})
	if err != nil || result.IsError {
		t.Fatalf("fs_search result=%+v err=%v", result, err)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Matches) != 3 || decoded.BinaryFilesSkipped != 1 {
		t.Fatalf("search result = %#v", decoded)
	}
	for _, match := range decoded.Matches {
		if strings.Contains(match.Path, ".git") || strings.Contains(match.Path, "linked.go") {
			t.Fatalf("unexpected match: %#v", match)
		}
	}
}

func TestFsSearchReportsMatchLimit(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("match\n", maxSearchMatches+5)
	if err := os.WriteFile(filepath.Join(root, "many.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := fsSearchHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"directory": ".", "regex": "match",
	}})
	if err != nil || result.IsError {
		t.Fatalf("search result=%+v err=%v", result, err)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Truncated || decoded.StoppedReason != "match_limit" || len(decoded.Matches) != maxSearchMatches {
		t.Fatalf("limited search = %#v", decoded)
	}
}

func TestFsSearchHonorsSharedOutputBudgetWithValidJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "many.txt"), []byte(strings.Repeat("match some long searchable text\n", 500)), 0o644); err != nil {
		t.Fatal(err)
	}
	request := &tools.Request{Arguments: map[string]any{"directory": ".", "regex": "match"}, MaxOutputChars: 4096}
	result, err := fsSearchHandler(NewExecutor(DefaultConfig()), root)(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("search result=%+v err=%v", result, err)
	}
	text := textResult(t, result)
	if len([]rune(text)) > int(request.MaxOutputChars) {
		t.Fatalf("result length = %d, budget = %d", len([]rune(text)), request.MaxOutputChars)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("budgeted result is invalid JSON: %v", err)
	}
	if !decoded.Truncated || decoded.StoppedReason != "output_size_limit" {
		t.Fatalf("budgeted search = %#v", decoded)
	}
}

func TestFsReadHasNoFilesystemSpecificTruncation(t *testing.T) {
	root := t.TempDir()
	var content strings.Builder
	for i := 0; i < MaxOutputLines+20; i++ {
		fmt.Fprintf(&content, "line-%03d\n", i)
	}
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := fsReadHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{"path": "large.txt"}})
	if err != nil || result.IsError || textResult(t, result) != content.String() {
		t.Fatalf("fs_read truncated content: result=%+v err=%v", result, err)
	}
}

func TestFsReadUsesSharedOutputBudgetAndReportsTruncation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("content\n", 2000)), 0o644); err != nil {
		t.Fatal(err)
	}
	request := &tools.Request{Arguments: map[string]any{"path": "large.txt"}, MaxOutputChars: 4096}
	result, err := fsReadHandler(NewExecutor(DefaultConfig()), root)(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("read result=%+v err=%v", result, err)
	}
	text := textResult(t, result)
	if len([]rune(text)) > int(request.MaxOutputChars) || !strings.Contains(text, "File content truncated by shared tool-result budget") {
		t.Fatalf("budgeted read length=%d text tail=%q", len([]rune(text)), text[len(text)-120:])
	}
}

func TestFsEditRequiresUniqueMatchAndPreservesMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.sh")
	if err := os.WriteFile(path, []byte("old\nold\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	handler := fsEditHandler(NewExecutor(DefaultConfig()), root)
	ambiguous, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "script.sh", "old_text": "old", "new_text": "new",
	}})
	if err != nil || !ambiguous.IsError || !strings.Contains(textResult(t, ambiguous), "matches 2 locations") {
		t.Fatalf("ambiguous edit result=%+v err=%v", ambiguous, err)
	}
	result, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "script.sh", "old_text": "old", "new_text": "new", "replace_all": true,
	}})
	if err != nil || result.IsError {
		t.Fatalf("replace-all result=%+v err=%v", result, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
}

func TestFsEditMatchesAcrossStreamingChunkBoundary(t *testing.T) {
	root := t.TempDir()
	prefix := strings.Repeat("x", 64*1024-2)
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte(prefix+"needle"+strings.Repeat("y", 64*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := fsEditHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "large.txt", "old_text": "needle", "new_text": "found",
	}})
	if err != nil || result.IsError {
		t.Fatalf("streaming edit result=%+v err=%v", result, err)
	}
	content, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(content, []byte("found")) || bytes.Contains(content, []byte("needle")) {
		t.Fatalf("streaming edit content mismatch: err=%v", err)
	}
}

func TestFsDeleteRequiresRecursiveAndProtectsRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := fsDeleteHandler(NewExecutor(DefaultConfig()), root)
	result, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{"path": "nested"}})
	if err != nil || !result.IsError {
		t.Fatalf("non-recursive delete result=%+v err=%v", result, err)
	}
	result, err = handler(context.Background(), &tools.Request{Arguments: map[string]any{"path": "nested", "recursive": true}})
	if err != nil || result.IsError {
		t.Fatalf("recursive delete result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatalf("nested directory still exists: %v", err)
	}
	result, err = handler(context.Background(), &tools.Request{Arguments: map[string]any{"path": ".", "recursive": true}})
	if err != nil || !result.IsError {
		t.Fatalf("root delete result=%+v err=%v", result, err)
	}
}

func TestFsDeleteUnlinksSymlinkWithoutDeletingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	result, err := fsDeleteHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{"path": "link.txt"}})
	if err != nil || result.IsError {
		t.Fatalf("delete link result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target was deleted: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link still exists: %v", err)
	}
}

func TestFsReadMissingPathSuggestsExistingSibling(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core", "actor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "core", "actor", "inbox.go"), []byte("package actor"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := fsReadHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "core/actor/inbox_test.go",
	}})
	if err != nil || !result.IsError {
		t.Fatalf("missing read result=%+v err=%v", result, err)
	}
	text := textResult(t, result)
	if !strings.Contains(text, "nearest existing directory") || !strings.Contains(text, "core/actor/inbox.go") {
		t.Fatalf("missing path error lacks recovery details: %q", text)
	}
}

func fsSearchHandler(exec *Executor, workdir string) tools.ToolHandlerFunc {
	return fsSearchFileSystemHandler(NewLocalFileSystem(exec, workdir))
}

func textResult(t *testing.T, result *tools.Result) string {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Content) != 1 {
		t.Fatalf("unexpected content length: %d", len(result.Content))
	}
	text, ok := result.Content[0].(tools.TextContent)
	if !ok {
		t.Fatalf("unexpected content type: %T", result.Content[0])
	}
	return text.Text
}

func TestFsListPathDefaultsToCurrentDirectory(t *testing.T) {
	tool := newFsListTool(nil, ".")
	path := tool.InputSchema.Properties["path"].(map[string]any)
	if path["default"] != "." {
		t.Fatalf("path default = %#v", path["default"])
	}
	for _, required := range tool.InputSchema.Required {
		if required == "path" {
			t.Fatal("path should be optional")
		}
	}
}

func TestFsWritePreservesEOFContent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	workdir := t.TempDir()

	handler := fsWriteHandler(NewExecutor(cfg), workdir)
	content := "line1\nEOF\nline3\n"

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]any{
			"path":    filepath.Join(workdir, "nested", "file.txt"),
			"content": content,
		},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", textResult(t, result))
	}

	data, err := os.ReadFile(filepath.Join(workdir, "nested", "file.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile() error: %v", err)
	}
	if string(data) != content {
		t.Fatalf("content mismatch: got %q want %q", string(data), content)
	}
}

func TestFsWritePreservesSingleQuotes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	workdir := t.TempDir()

	handler := fsWriteHandler(NewExecutor(cfg), workdir)
	content := "can't\nwon't\n"

	result, err := handler(context.Background(), &tools.Request{
		Arguments: map[string]any{
			"path":    filepath.Join(workdir, "quotes.txt"),
			"content": content,
		},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %s", textResult(t, result))
	}

	data, err := os.ReadFile(filepath.Join(workdir, "quotes.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile() error: %v", err)
	}
	if string(data) != content {
		t.Fatalf("content mismatch: got %q want %q", string(data), content)
	}
}

func TestFsWritePreservesExistingMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.sh")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := fsWriteHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "script.sh", "content": "new",
	}})
	if err != nil || result.IsError {
		t.Fatalf("write result=%+v err=%v", result, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
}

func TestResolveToolPathRejectsDeniedPath(t *testing.T) {
	cfg := DefaultConfig()
	workdir := t.TempDir()
	denyRoot := filepath.Join(workdir, "secret")
	cfg.Sandbox.Filesystem.Deny = []string{denyRoot}

	_, err := resolveToolPath(cfg, workdir, filepath.Join(denyRoot, "data.txt"), fsAccessRead)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denied error, got %v", err)
	}
}

func TestResolveToolPathAllowsOutsideAndDeniedPathsWhenIsolationDisabled(t *testing.T) {
	cfg := DefaultConfig()
	workdir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "data.txt")
	cfg.Sandbox.Filesystem.Deny = []string{outside}
	cfg.DisableIsolation()

	got, err := resolveToolPath(cfg, workdir, target, fsAccessWrite)
	if err != nil {
		t.Fatalf("resolveToolPath() error = %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks() error = %v", err)
	}
	want := filepath.Join(wantRoot, "data.txt")
	if got != want {
		t.Fatalf("resolveToolPath() = %q, want %q", got, want)
	}
}

func TestResolveToolPathAllowsProtectedReadButRejectsWrite(t *testing.T) {
	cfg := DefaultConfig()
	workdir := t.TempDir()
	protectedRoot := filepath.Join(workdir, "protected")
	cfg.Sandbox.Filesystem.Protected = []string{protectedRoot}

	readPath := filepath.Join(protectedRoot, "data.txt")
	if _, err := resolveToolPath(cfg, workdir, readPath, fsAccessRead); err != nil {
		t.Fatalf("read should be allowed: %v", err)
	}

	if _, err := resolveToolPath(cfg, workdir, readPath, fsAccessWrite); err == nil {
		t.Fatal("expected write rejection")
	}
}

func TestFsHandlersUseSharedAccessRules(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	workdir := t.TempDir()
	protectedRoot := filepath.Join(workdir, "protected")
	readOnlyRoot := filepath.Join(workdir, "readonly")
	writeRoot := filepath.Join(workdir, "allowed-write")
	cfg.Sandbox.Filesystem.Protected = []string{protectedRoot}
	cfg.Sandbox.Filesystem.ReadOnly = []string{readOnlyRoot}
	cfg.Sandbox.Filesystem.Write = append(cfg.Sandbox.Filesystem.Write, writeRoot)

	if err := os.MkdirAll(protectedRoot, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error: %v", err)
	}
	if err := os.MkdirAll(readOnlyRoot, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(protectedRoot, "edit.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(readOnlyRoot, "read.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	exec := NewExecutor(cfg)

	readResult, err := fsReadHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{"path": filepath.Join(readOnlyRoot, "read.txt")},
	})
	if err != nil {
		t.Fatalf("fsReadHandler() error: %v", err)
	}
	if readResult.IsError {
		t.Fatalf("read should be allowed: %s", textResult(t, readResult))
	}

	editResult, err := fsEditHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{
			"path":     filepath.Join(protectedRoot, "edit.txt"),
			"old_text": "hello",
			"new_text": "world",
		},
	})
	if err != nil {
		t.Fatalf("fsEditHandler() error: %v", err)
	}
	if !editResult.IsError {
		t.Fatal("expected protected edit to be rejected")
	}

	listResult, err := fsListHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{"path": readOnlyRoot},
	})
	if err != nil {
		t.Fatalf("fsListHandler() error: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("list should be allowed: %s", textResult(t, listResult))
	}

	deleteTarget := filepath.Join(writeRoot, "delete-me")
	if err := os.MkdirAll(writeRoot, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(deleteTarget, []byte("bye"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	deleteResult, err := fsDeleteHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{"path": deleteTarget},
	})
	if err != nil {
		t.Fatalf("fsDeleteHandler() error: %v", err)
	}
	if deleteResult.IsError {
		t.Fatalf("delete should be allowed: %s", textResult(t, deleteResult))
	}
}

func TestFsReadRejectsSymlinkEscape(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	workdir := t.TempDir()
	exec := NewExecutor(cfg)

	// A symlink planted inside the workdir that points at a sensitive host
	// file must not be readable through the fs tools.
	link := filepath.Join(workdir, "leak")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatalf("os.Symlink() error: %v", err)
	}

	if _, err := resolveToolPath(cfg, workdir, link, fsAccessRead); err == nil {
		t.Fatal("expected symlink outside the workdir to be rejected")
	}

	result, err := fsReadHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{"path": link},
	})
	if err != nil {
		t.Fatalf("fsReadHandler() error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected fs_read of an escaping symlink to fail")
	}
}

func TestFsWriteRejectsSymlinkEscape(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	workdir := t.TempDir()
	exec := NewExecutor(cfg)

	outside := t.TempDir()
	link := filepath.Join(workdir, "escape")
	if err := os.Symlink(filepath.Join(outside, "pwned.txt"), link); err != nil {
		t.Fatalf("os.Symlink() error: %v", err)
	}

	if _, err := resolveToolPath(cfg, workdir, link, fsAccessWrite); err == nil {
		t.Fatal("expected dangling symlink outside the workdir to be rejected")
	}

	result, err := fsWriteHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{"path": link, "content": "nope"},
	})
	if err != nil {
		t.Fatalf("fsWriteHandler() error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected fs_write through an escaping symlink to fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("the symlink target must not have been written")
	}
}

func TestFsWriteDoesNotFollowParentChangedAfterResolve(t *testing.T) {
	workdir := t.TempDir()
	outside := t.TempDir()
	parent := filepath.Join(workdir, "safe")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}

	fs := NewLocalFileSystem(NewExecutor(DefaultConfig()), workdir)
	resolved, err := fs.Resolve(context.Background(), "safe/file.txt", FileAccessWrite)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if err := os.Rename(parent, filepath.Join(workdir, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if err := fs.WriteFile(context.Background(), resolved, []byte("blocked")); err == nil {
		t.Fatal("expected rooted write to reject a parent symlink escaping the workdir")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file must not be created: %v", err)
	}
}

func TestFsReadAllowsRegularNestedPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	workdir := t.TempDir()
	exec := NewExecutor(cfg)

	nested := filepath.Join(workdir, "a", "b", "file.txt")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error: %v", err)
	}
	if err := os.WriteFile(nested, []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	got, err := resolveToolPath(cfg, workdir, nested, fsAccessRead)
	if err != nil {
		t.Fatalf("resolveToolPath() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks() error = %v", err)
	}
	if got != want {
		t.Fatalf("resolveToolPath() = %q, want %q", got, want)
	}

	result, err := fsReadHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{"path": nested},
	})
	if err != nil {
		t.Fatalf("fsReadHandler() error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected fs_read error: %s", textResult(t, result))
	}
}
