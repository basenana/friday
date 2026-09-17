package logger

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestIsFileBacked(t *testing.T) {
	InitWithFile(filepath.Join(t.TempDir(), "friday.log"))
	defer Close()

	if !IsFileBacked() {
		t.Fatal("expected file-backed logger")
	}
}

func TestIsFileBackedReportsFallback(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	InitWithFile(filepath.Join(blocker, "friday.log"))
	defer Close()

	if IsFileBacked() {
		t.Fatal("failed file initialization must not report file backing")
	}
}

func TestFileLoggerFailureDoesNotFallBackToStdout(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	previousStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = previousStdout
		_ = reader.Close()
		_ = writer.Close()
	})

	InitWithFile(filepath.Join(blocker, "friday.log"))
	New("test").Info("must not reach the terminal")
	Close()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 0 {
		t.Fatalf("stdout = %q, want no logger fallback output", output)
	}
}
