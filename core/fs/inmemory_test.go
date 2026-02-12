package fs

import (
	"slices"
	"testing"
)

func TestInMemory(t *testing.T) {
	fs := NewInMemory()

	t.Run("Write and Read", func(t *testing.T) {
		err := fs.Write("/test/file.txt", "hello world")
		if err != nil {
			t.Fatal(err)
		}

		content, err := fs.Read("/test/file.txt")
		if err != nil {
			t.Fatal(err)
		}
		if content != "hello world" {
			t.Fatalf("expected hello world, got %s", content)
		}
	})

	t.Run("Read non-existent file", func(t *testing.T) {
		_, err := fs.Read("/nonexistent/file.txt")
		if err == nil {
			t.Fatal("expected error for non-existent file")
		}
	})

	t.Run("MkdirAll", func(t *testing.T) {
		err := fs.MkdirAll("/new/dir")
		if err != nil {
			t.Fatal(err)
		}

		entries, err := fs.Ls("/new")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(entries, "dir") {
			t.Fatalf("expected entries to contain dir, got %v", entries)
		}
	})

	t.Run("MkdirAll idempotent", func(t *testing.T) {
		err := fs.MkdirAll("/existing/dir")
		if err != nil {
			t.Fatal(err)
		}

		err = fs.MkdirAll("/existing/dir")
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Ls", func(t *testing.T) {
		if err := fs.Write("/ls/test1.txt", "content1"); err != nil {
			t.Fatal(err)
		}
		if err := fs.Write("/ls/test2.txt", "content2"); err != nil {
			t.Fatal(err)
		}
		if err := fs.MkdirAll("/ls/subdir"); err != nil {
			t.Fatal(err)
		}

		entries, err := fs.Ls("/ls")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}
		if !slices.Contains(entries, "test1.txt") {
			t.Fatalf("expected entries to contain test1.txt, got %v", entries)
		}
		if !slices.Contains(entries, "test2.txt") {
			t.Fatalf("expected entries to contain test2.txt, got %v", entries)
		}
		if !slices.Contains(entries, "subdir") {
			t.Fatalf("expected entries to contain subdir, got %v", entries)
		}
	})

	t.Run("Ls non-existent dir", func(t *testing.T) {
		_, err := fs.Ls("/nonexistent")
		if err == nil {
			t.Fatal("expected error for non-existent dir")
		}
	})

	t.Run("Delete file", func(t *testing.T) {
		if err := fs.Write("/delete/file.txt", "content"); err != nil {
			t.Fatal(err)
		}
		if err := fs.Delete("/delete/file.txt"); err != nil {
			t.Fatal(err)
		}

		_, err := fs.Read("/delete/file.txt")
		if err == nil {
			t.Fatal("expected error after deleting file")
		}
	})

	t.Run("Delete directory", func(t *testing.T) {
		if err := fs.MkdirAll("/delete/dir"); err != nil {
			t.Fatal(err)
		}
		if err := fs.Delete("/delete/dir"); err != nil {
			t.Fatal(err)
		}

		_, err := fs.Ls("/delete/dir")
		if err == nil {
			t.Fatal("expected error after deleting dir")
		}
	})

	t.Run("Delete non-existent path", func(t *testing.T) {
		err := fs.Delete("/nonexistent")
		if err == nil {
			t.Fatal("expected error for non-existent path")
		}
	})

	t.Run("Write updates parent dir listing", func(t *testing.T) {
		if err := fs.Write("/parent/child.txt", "content"); err != nil {
			t.Fatal(err)
		}

		entries, err := fs.Ls("/parent")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(entries, "child.txt") {
			t.Fatalf("expected entries to contain child.txt, got %v", entries)
		}
	})

	t.Run("Delete removes from parent dir listing", func(t *testing.T) {
		if err := fs.Write("/parent2/file.txt", "content"); err != nil {
			t.Fatal(err)
		}
		if err := fs.Delete("/parent2/file.txt"); err != nil {
			t.Fatal(err)
		}

		entries, err := fs.Ls("/parent2")
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(entries, "file.txt") {
			t.Fatalf("expected entries to NOT contain file.txt, got %v", entries)
		}
	})
}
