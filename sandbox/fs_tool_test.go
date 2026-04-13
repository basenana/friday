package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/core/tools"
)

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
			"path":           filepath.Join(protectedRoot, "edit.txt"),
			"search_string":  "hello",
			"replace_string": "world",
		},
	})
	if err != nil {
		t.Fatalf("fsEditHandler() error: %v", err)
	}
	if !editResult.IsError {
		t.Fatal("expected protected edit to be rejected")
	}

	mkdirResult, err := fsMkdirHandler(exec, workdir)(context.Background(), &tools.Request{
		Arguments: map[string]any{"path": filepath.Join(writeRoot, "nested")},
	})
	if err != nil {
		t.Fatalf("fsMkdirHandler() error: %v", err)
	}
	if mkdirResult.IsError {
		t.Fatalf("mkdir should be allowed: %s", textResult(t, mkdirResult))
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
