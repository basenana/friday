package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestFsListPreservesLogicalPathForAllowedSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.ReadOnly = append(cfg.Sandbox.Filesystem.ReadOnly, target)
	result, err := fsListHandler(NewExecutor(cfg), root)(context.Background(), &tools.Request{Arguments: map[string]any{"path": "linked"}})
	if err != nil || result.IsError {
		t.Fatalf("fs_list result=%+v err=%v", result, err)
	}
	var decoded fsListResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Path != "linked" || len(decoded.Entries) != 1 || decoded.Entries[0].Path != "linked/file.txt" {
		t.Fatalf("logical list paths not preserved: %#v", decoded)
	}
	if strings.Contains(textResult(t, result), filepath.ToSlash(target)) {
		t.Fatalf("physical symlink target leaked: %s", textResult(t, result))
	}
}

func TestFsSearchPreservesLogicalPathForAllowedSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "result.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.ReadOnly = append(cfg.Sandbox.Filesystem.ReadOnly, target)
	result, err := fsSearchHandler(NewExecutor(cfg), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"directory": "linked", "regex": "needle",
	}})
	if err != nil || result.IsError {
		t.Fatalf("fs_search result=%+v err=%v", result, err)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Directory != "linked" || len(decoded.Matches) != 1 || decoded.Matches[0].Path != "linked/nested/result.txt" {
		t.Fatalf("logical search paths not preserved: %#v", decoded)
	}
	if strings.Contains(textResult(t, result), filepath.ToSlash(target)) {
		t.Fatalf("physical symlink target leaked: %s", textResult(t, result))
	}
}

func TestFsToolsPreserveLogicalPathsInMissingSymlinkErrors(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "missing.go"), []byte("package linked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.ReadOnly = append(cfg.Sandbox.Filesystem.ReadOnly, target)
	exec := NewExecutor(cfg)
	requests := []struct {
		name    string
		handler tools.ToolHandlerFunc
		args    map[string]any
	}{
		{name: "list", handler: fsListHandler(exec, root), args: map[string]any{"path": "linked/missing.txt"}},
		{name: "search", handler: fsSearchHandler(exec, root), args: map[string]any{"directory": "linked/missing.txt", "regex": "needle"}},
	}
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.handler(context.Background(), &tools.Request{Arguments: test.args})
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			text := textResult(t, result)
			if !strings.Contains(text, `nearest existing directory is "linked"`) || !strings.Contains(text, "linked/missing.go") {
				t.Fatalf("logical recovery paths missing: %q", text)
			}
			if strings.Contains(text, filepath.ToSlash(target)) {
				t.Fatalf("physical symlink target leaked: %q", text)
			}
		})
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

func TestFsSearchRealWorldPatternsReturnPathsAndLineNumbers(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"internal/pipeline/publish.go": strings.Join([]string{
			"package pipeline",
			"// publish with pipeline_publish_draft",
			"// pipeline_publish_draft 完成",
		}, "\n"),
		"internal/systemagent/scope.go": strings.Join([]string{
			"package systemagent",
			"var operation = MutationScopeOperationPipelineImport",
			"scope := MutationScope{}",
			"scope = NewMutationScope(operation)",
		}, "\n"),
		"internal/systemagent/hpcasset/tools.go": strings.Join([]string{
			"package hpcasset",
			"// keeps only the read-only surface",
		}, "\n"),
		"internal/worker/jobs.go": strings.Join([]string{
			"package worker",
			"func syncJobRecord() {}",
			"func applyJobRecordMetadata() {}",
			`metadata["attempt"] = job.Attempt`,
			`metadata["submission_invocation_id"] = invocationID`,
			"if job.Required { return }",
		}, "\n"),
		"internal/docs/mutation.txt": strings.Join([]string{
			"MutationScope is the machine-verifiable boundary",
			"write operations are excluded from the toolset entirely",
		}, "\n"),
		"bioclaw/retry.go": strings.Join([]string{
			"package bioclaw",
			`const submission = "submission_invocation_id"`,
			`const retry = "retry_of_submission_invocation_id"`,
		}, "\n"),
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

	tests := []struct {
		name      string
		directory string
		pattern   string
		want      []fsSearchMatch
	}{
		{
			name: "publish alternatives with Chinese text", directory: "internal",
			pattern: `publish with pipeline_publish_draft|pipeline_publish_draft 完成|publish_draft 完成`,
			want: []fsSearchMatch{
				{Path: "internal/pipeline/publish.go", Line: 2, Column: 4, Text: "// publish with pipeline_publish_draft"},
				{Path: "internal/pipeline/publish.go", Line: 3, Column: 4, Text: "// pipeline_publish_draft 完成"},
			},
		},
		{
			name: "operation name", directory: "internal/systemagent",
			pattern: `MutationScopeOperationPipelineImport`,
			want:    []fsSearchMatch{{Path: "internal/systemagent/scope.go", Line: 2, Column: 17, Text: "var operation = MutationScopeOperationPipelineImport"}},
		},
		{
			name: "worker functions and metadata", directory: filepath.Join(root, "internal", "worker"),
			pattern: `func syncJobRecord|func applyJobRecordMetadata|metadata\["(attempt|required|retry_of_submission_invocation_id|submission_invocation_id|required_source|exit_code)"\]|job\.Attempt|job\.Required\b`,
			want: []fsSearchMatch{
				{Path: "internal/worker/jobs.go", Line: 2, Column: 1, Text: "func syncJobRecord() {}"},
				{Path: "internal/worker/jobs.go", Line: 3, Column: 1, Text: "func applyJobRecordMetadata() {}"},
				{Path: "internal/worker/jobs.go", Line: 4, Column: 1, Text: `metadata["attempt"] = job.Attempt`},
				{Path: "internal/worker/jobs.go", Line: 5, Column: 1, Text: `metadata["submission_invocation_id"] = invocationID`},
				{Path: "internal/worker/jobs.go", Line: 6, Column: 4, Text: "if job.Required { return }"},
			},
		},
		{
			name: "mutation scope constructors", directory: "internal",
			pattern: `MutationScope\{|NewMutationScope|MutationScopeOperation`,
			want: []fsSearchMatch{
				{Path: "internal/systemagent/scope.go", Line: 2, Column: 17, Text: "var operation = MutationScopeOperationPipelineImport"},
				{Path: "internal/systemagent/scope.go", Line: 3, Column: 10, Text: "scope := MutationScope{}"},
				{Path: "internal/systemagent/scope.go", Line: 4, Column: 9, Text: "scope = NewMutationScope(operation)"},
			},
		},
		{
			name: "documentation phrases", directory: "internal",
			pattern: `MutationScope is the machine-verifiable|excluded from the toolset entirely`,
			want: []fsSearchMatch{
				{Path: "internal/docs/mutation.txt", Line: 1, Column: 1, Text: "MutationScope is the machine-verifiable boundary"},
				{Path: "internal/docs/mutation.txt", Line: 2, Column: 22, Text: "write operations are excluded from the toolset entirely"},
			},
		},
		{
			name: "read-only surface", directory: "internal/systemagent/hpcasset",
			pattern: `keeps only the read-only surface`,
			want:    []fsSearchMatch{{Path: "internal/systemagent/hpcasset/tools.go", Line: 2, Column: 4, Text: "// keeps only the read-only surface"}},
		},
		{
			name: "submission identifiers", directory: filepath.Join(root, "bioclaw"),
			pattern: `submission_invocation_id|retry_of_submission_invocation_id`,
			want: []fsSearchMatch{
				{Path: "bioclaw/retry.go", Line: 2, Column: 21, Text: `const submission = "submission_invocation_id"`},
				{Path: "bioclaw/retry.go", Line: 3, Column: 16, Text: `const retry = "retry_of_submission_invocation_id"`},
			},
		},
	}

	handler := fsSearchHandler(NewExecutor(DefaultConfig()), root)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{
				"directory": test.directory, "regex": test.pattern,
			}})
			if err != nil || result.IsError {
				t.Fatalf("fs_search result=%+v err=%v", result, err)
			}
			var decoded fsSearchResult
			if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(decoded.Matches) != fmt.Sprint(test.want) {
				t.Fatalf("matches = %#v, want %#v", decoded.Matches, test.want)
			}
		})
	}
}

func TestFsSearchUsesUnicodeColumnsAndStablePathOrder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"z/result.txt", "a/result.txt"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("前缀 match\r\nlast match"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := fsSearchHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"directory": ".", "regex": "match",
	}})
	if err != nil || result.IsError {
		t.Fatalf("fs_search result=%+v err=%v", result, err)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"a/result.txt", "a/result.txt", "z/result.txt", "z/result.txt"}
	for i, match := range decoded.Matches {
		if match.Path != wantPaths[i] {
			t.Fatalf("match[%d].Path = %q, want %q", i, match.Path, wantPaths[i])
		}
		if match.Line == 1 && match.Column != 4 {
			t.Fatalf("Unicode column = %d, want 4", match.Column)
		}
	}
}

func TestFsListOutsideWorkdirReturnsAbsolutePaths(t *testing.T) {
	workdir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "result.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	result, err := fsListHandler(NewExecutor(cfg), workdir)(context.Background(), &tools.Request{Arguments: map[string]any{"path": outside}})
	if err != nil || result.IsError {
		t.Fatalf("fs_list result=%+v err=%v", result, err)
	}
	var decoded fsListResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.ToSlash(filepath.Clean(outside))
	if decoded.Path != wantRoot || len(decoded.Entries) != 1 || decoded.Entries[0].Path != wantRoot+"/result.txt" {
		t.Fatalf("list = %#v, want root %q", decoded, wantRoot)
	}
}

func TestFsSearchOutsideWorkdirReturnsAbsolutePaths(t *testing.T) {
	workdir := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "nested", "result.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.DisableIsolation()
	result, err := fsSearchHandler(NewExecutor(cfg), workdir)(context.Background(), &tools.Request{Arguments: map[string]any{
		"directory": outside, "regex": "needle",
	}})
	if err != nil || result.IsError {
		t.Fatalf("fs_search result=%+v err=%v", result, err)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(filepath.Clean(path))
	if len(decoded.Matches) != 1 || decoded.Matches[0].Path != want {
		t.Fatalf("matches = %#v, want path %q", decoded.Matches, want)
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

// blockingOpenFileSystem stalls Open on one chosen path until the context is
// cancelled, which lets the cancellation test stop the search mid-scan
// deterministically instead of racing a timer against a fast tree.
type blockingOpenFileSystem struct {
	*localFileSystem
	blockPath string
}

func (f blockingOpenFileSystem) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	if path == f.blockPath {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.localFileSystem.Open(ctx, path)
}

func TestFsSearchCancellationStopsPromptly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "aaa_blocker.txt"), []byte("needle\nplain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "zzz_match.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := blockingOpenFileSystem{
		localFileSystem: &localFileSystem{exec: NewExecutor(DefaultConfig()), workdir: root},
	}
	// Resolve through the same path machinery the walker uses, so the blocked
	// path matches even when TMPDIR itself is behind a symlink.
	resolvedBlocker, err := fs.Resolve(context.Background(), filepath.Join(root, "aaa_blocker.txt"), FileAccessRead)
	if err != nil {
		t.Fatal(err)
	}
	fs.blockPath = resolvedBlocker
	handler := fsSearchFileSystemHandler(fs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result *tools.Result
		err    error
	}
	done := make(chan outcome, 1)
	start := time.Now()
	go func() {
		result, err := handler(ctx, &tools.Request{Arguments: map[string]any{"directory": ".", "regex": "needle"}})
		done <- outcome{result: result, err: err}
	}()

	// The pool cannot finish while a worker is parked inside the blocking
	// Open, so cancelling here always interrupts a live search.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("handler error: %v", out.err)
		}
		if !out.result.IsError {
			t.Fatalf("cancelled search must surface an error result, got: %s", textResult(t, out.result))
		}
		if text := textResult(t, out.result); !strings.Contains(text, "context canceled") {
			t.Fatalf("cancelled search error = %q, want it to mention context canceled", text)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("cancelled search took %s to return", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled search did not return within 10s")
	}
}

func TestFsSearchErrorsSkippedIncrement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based skip rules are unix-only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions; skip accounting would not trigger")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readable.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockedDir := filepath.Join(root, "locked")
	if err := os.MkdirAll(filepath.Join(lockedDir, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockedDir, "inner", "hidden.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "locked.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Restore permissions so t.TempDir cleanup can remove the tree.
	t.Cleanup(func() {
		_ = os.Chmod(lockedDir, 0o755)
		_ = os.Chmod(filepath.Join(root, "locked.txt"), 0o644)
	})
	if err := os.Chmod(lockedDir, 0o000); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}
	if err := os.Chmod(filepath.Join(root, "locked.txt"), 0o000); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}

	result, err := fsSearchHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"directory": ".", "regex": "needle",
	}})
	if err != nil || result.IsError {
		t.Fatalf("fs_search result=%+v err=%v", result, err)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ErrorsSkipped < 2 {
		t.Fatalf("errors_skipped = %d, want at least 2 (unreadable dir + unreadable file): %#v", decoded.ErrorsSkipped, decoded)
	}
	if len(decoded.Matches) != 1 || decoded.Matches[0].Path != "readable.txt" {
		t.Fatalf("matches = %#v, want only readable.txt", decoded.Matches)
	}
}

func TestFsSearchParallelOrderStable(t *testing.T) {
	root := t.TempDir()
	for dir := 0; dir < 6; dir++ {
		name := fmt.Sprintf("pkg%d", dir)
		for file := 0; file < 10; file++ {
			content := fmt.Sprintf("plain line\nneedle in %s/%d\nanother needle tail\n", name, file)
			path := filepath.Join(root, name, fmt.Sprintf("file%02d.txt", file))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	handler := fsSearchHandler(NewExecutor(DefaultConfig()), root)

	var first string
	for run := 0; run < 5; run++ {
		result, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{
			"directory": ".", "regex": "needle",
		}})
		if err != nil || result.IsError {
			t.Fatalf("fs_search result=%+v err=%v", result, err)
		}
		var decoded fsSearchResult
		if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Matches) != 120 {
			t.Fatalf("matches = %d, want 120", len(decoded.Matches))
		}
		for i := 1; i < len(decoded.Matches); i++ {
			prev, cur := decoded.Matches[i-1], decoded.Matches[i]
			if prev.Path > cur.Path || (prev.Path == cur.Path && prev.Line > cur.Line) {
				t.Fatalf("matches out of order at %d: %#v then %#v", i, prev, cur)
			}
		}
		encoded := fmt.Sprint(decoded.Matches)
		if first == "" {
			first = encoded
			continue
		}
		if encoded != first {
			t.Fatalf("run %d produced a different match order than run 0", run)
		}
	}
}

func TestFsSearchTruncationRespectsLimits(t *testing.T) {
	root := t.TempDir()
	for file := 0; file < 5; file++ {
		content := strings.Repeat("needle line\n", 300)
		name := fmt.Sprintf("batch%d.txt", file)
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := fsSearchHandler(NewExecutor(DefaultConfig()), root)(context.Background(), &tools.Request{Arguments: map[string]any{
		"directory": ".", "regex": "needle",
	}})
	if err != nil || result.IsError {
		t.Fatalf("fs_search result=%+v err=%v", result, err)
	}
	var decoded fsSearchResult
	if err := json.Unmarshal([]byte(textResult(t, result)), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Truncated {
		t.Fatalf("multi-file overflow must report truncated: %#v", decoded)
	}
	if decoded.StoppedReason != "match_limit" && decoded.StoppedReason != "output_size_limit" {
		t.Fatalf("stopped_reason = %q, want match_limit or output_size_limit", decoded.StoppedReason)
	}
	if len(decoded.Matches) > maxSearchMatches {
		t.Fatalf("matches = %d, want at most %d", len(decoded.Matches), maxSearchMatches)
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

func TestFsReadLineRangeSchemaIsOptional(t *testing.T) {
	tool := newFsReadTool(nil, ".")
	for _, name := range []string{"start_line", "end_line"} {
		property, ok := tool.InputSchema.Properties[name].(map[string]any)
		if !ok || property["type"] != "integer" || property["minimum"] != float64(1) {
			t.Fatalf("%s schema = %#v", name, property)
		}
		for _, required := range tool.InputSchema.Required {
			if required == name {
				t.Fatalf("%s must be optional", name)
			}
		}
	}
}

func TestFsReadSupportsInclusiveLineRanges(t *testing.T) {
	root := t.TempDir()
	content := "one\n二号\r\nthree\nfour"
	if err := os.WriteFile(filepath.Join(root, "lines.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := fsReadHandler(NewExecutor(DefaultConfig()), root)
	tests := []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{name: "full file by default", arguments: map[string]any{}, want: content},
		{name: "closed range", arguments: map[string]any{"start_line": float64(2), "end_line": float64(3)}, want: "二号\r\nthree\n"},
		{name: "from start through EOF", arguments: map[string]any{"start_line": float64(3)}, want: "three\nfour"},
		{name: "from beginning through end", arguments: map[string]any{"end_line": float64(2)}, want: "one\n二号\r\n"},
		{name: "single final line", arguments: map[string]any{"start_line": float64(4), "end_line": float64(4)}, want: "four"},
		{name: "end beyond EOF", arguments: map[string]any{"end_line": float64(99)}, want: content},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.arguments["path"] = "lines.txt"
			result, err := handler(context.Background(), &tools.Request{Arguments: test.arguments})
			if err != nil || result.IsError {
				t.Fatalf("fs_read result=%+v err=%v", result, err)
			}
			if got := textResult(t, result); got != test.want {
				t.Fatalf("content = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFsReadRejectsInvalidOrOutOfRangeLines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "lines.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := fsReadHandler(NewExecutor(DefaultConfig()), root)
	tests := []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{name: "zero", arguments: map[string]any{"start_line": float64(0)}, want: "start_line must be a positive integer"},
		{name: "negative", arguments: map[string]any{"end_line": float64(-1)}, want: "end_line must be a positive integer"},
		{name: "fraction", arguments: map[string]any{"start_line": 1.5}, want: "start_line must be a positive integer"},
		{name: "wrong type", arguments: map[string]any{"end_line": "2"}, want: "end_line must be a positive integer"},
		{name: "reversed", arguments: map[string]any{"start_line": float64(3), "end_line": float64(2)}, want: "start_line must be less than or equal to end_line"},
		{name: "past EOF", arguments: map[string]any{"start_line": float64(4)}, want: "start_line 4 exceeds the file's 3 lines"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.arguments["path"] = "lines.txt"
			result, err := handler(context.Background(), &tools.Request{Arguments: test.arguments})
			if err != nil || !result.IsError {
				t.Fatalf("fs_read result=%+v err=%v", result, err)
			}
			if got := textResult(t, result); !strings.Contains(got, test.want) {
				t.Fatalf("error = %q, want substring %q", got, test.want)
			}
		})
	}
}

func TestFsReadLineRangeHandlesEmptyAndLongFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	longLine := strings.Repeat("界", 70*1024)
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte("first\n"+longLine+"\nlast"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := fsReadHandler(NewExecutor(DefaultConfig()), root)

	empty, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "empty.txt", "end_line": float64(10),
	}})
	if err != nil || empty.IsError || textResult(t, empty) != "" {
		t.Fatalf("empty range result=%+v err=%v", empty, err)
	}
	pastEmpty, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "empty.txt", "start_line": float64(1),
	}})
	if err != nil || !pastEmpty.IsError || !strings.Contains(textResult(t, pastEmpty), "file's 0 lines") {
		t.Fatalf("empty start result=%+v err=%v", pastEmpty, err)
	}
	long, err := handler(context.Background(), &tools.Request{Arguments: map[string]any{
		"path": "long.txt", "start_line": float64(2), "end_line": float64(2),
	}})
	if err != nil || long.IsError || textResult(t, long) != longLine+"\n" {
		t.Fatalf("long-line result length=%d error=%v tool_error=%v", len(textResult(t, long)), err, long.IsError)
	}
}

func TestFsReadLineRangeHonorsSharedOutputBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "unicode.txt"), []byte("skip\n"+strings.Repeat("界", 1000)+"\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := &tools.Request{
		Arguments:      map[string]any{"path": "unicode.txt", "start_line": float64(2)},
		MaxOutputChars: 300,
	}
	result, err := fsReadHandler(NewExecutor(DefaultConfig()), root)(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("fs_read result=%+v err=%v", result, err)
	}
	text := textResult(t, result)
	if len([]rune(text)) > int(request.MaxOutputChars) || !strings.Contains(text, "File content truncated by shared tool-result budget") {
		t.Fatalf("budgeted line range length=%d text=%q", len([]rune(text)), text)
	}
}

type countingReadCloser struct {
	reader *strings.Reader
	read   int
}

func (r *countingReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += n
	return n, err
}

func (r *countingReadCloser) Close() error { return nil }

type trackingOpenFileSystem struct {
	FileSystem
	reader io.ReadCloser
}

func (f trackingOpenFileSystem) Open(context.Context, string) (io.ReadCloser, error) {
	return f.reader, nil
}

func TestFsReadLineRangeStopsAfterRequestedEnd(t *testing.T) {
	content := "first\n" + strings.Repeat("unrequested trailing data\n", 1000)
	reader := &countingReadCloser{reader: strings.NewReader(content)}
	fs := trackingOpenFileSystem{reader: reader}
	got, truncated, linesRead, err := readFileLineRangeWithinBudget(context.Background(), fs, "unused", 1, true, 1, true, 0)
	if err != nil || truncated || linesRead != 1 || string(got) != "first\n" {
		t.Fatalf("range result=%q truncated=%v lines=%d err=%v", got, truncated, linesRead, err)
	}
	if reader.read >= len(content) {
		t.Fatalf("range reader consumed the complete file: %d bytes", reader.read)
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
