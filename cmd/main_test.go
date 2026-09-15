package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/utils/logger"
)

func TestLogOuterSandboxWarningWritesFileOnce(t *testing.T) {
	t.Setenv("IS_SANDBOX", "1")
	path := filepath.Join(t.TempDir(), "friday.log")
	logger.InitWithFile(path)

	logOuterSandboxWarning()
	logger.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if count := strings.Count(string(content), outerSandboxWarning); count != 1 {
		t.Fatalf("warning count = %d, want 1; log=%s", count, content)
	}
}

func TestLogOuterSandboxWarningIgnoresOtherValues(t *testing.T) {
	t.Setenv("IS_SANDBOX", "true")
	path := filepath.Join(t.TempDir(), "friday.log")
	logger.InitWithFile(path)

	logOuterSandboxWarning()
	logger.Close()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if strings.Contains(string(content), outerSandboxWarning) {
		t.Fatalf("unexpected warning in log: %s", content)
	}
}

func TestLogOuterSandboxWarningDoesNotUseStdoutFallback(t *testing.T) {
	t.Setenv("IS_SANDBOX", "1")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	previousStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = previousStdout
		_ = reader.Close()
		_ = writer.Close()
	})

	logger.InitWithFile(filepath.Join(blocker, "friday.log"))
	logOuterSandboxWarning()
	logger.Close()
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if len(output) != 0 {
		t.Fatalf("stdout = %q, want empty", output)
	}
}
