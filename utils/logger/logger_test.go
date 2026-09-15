package logger

import (
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
		t.Fatal("expected stdout fallback not to report file backing")
	}
}
