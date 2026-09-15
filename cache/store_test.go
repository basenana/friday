package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreFreshStaleAndPrune(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewFileStore(t.TempDir(), WithClock(func() time.Time { return now }))
	if err := store.Put(context.Background(), "mcp", "server", map[string]string{"tool": "search"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if status, err := store.Get(context.Background(), "mcp", "server", &got); err != nil || status != StatusFresh || got["tool"] != "search" {
		t.Fatalf("fresh get = %v, %v, %v", status, got, err)
	}
	now = now.Add(2 * time.Hour)
	if status, err := store.Get(context.Background(), "mcp", "server", &got); err != nil || status != StatusStale {
		t.Fatalf("stale get = %v, %v", status, err)
	}
	if err := store.Prune(context.Background(), "mcp"); err != nil {
		t.Fatal(err)
	}
	if status, err := store.Get(context.Background(), "mcp", "server", &got); err != nil || status != StatusMiss {
		t.Fatalf("pruned get = %v, %v", status, err)
	}
}

func TestFileStoreRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	store := NewFileStore(root)
	if err := store.Put(context.Background(), "../bad", "key", 1, 0); err == nil {
		t.Fatal("expected traversal rejection")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "mcp")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp", "key", 1, 0); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestFileStoreUsesPrivatePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caches")
	store := NewFileStore(root)
	if err := store.Put(context.Background(), "mcp", "server", 1, 0); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "mcp", "server.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("entry mode = %v, err=%v", info.Mode().Perm(), err)
	}
}

func TestFileStoreLockHonorsContext(t *testing.T) {
	store := NewFileStore(t.TempDir())
	unlock, err := store.lock(context.Background(), "mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var value int
	if _, err := store.Get(ctx, "mcp", "entry", &value); err == nil {
		t.Fatal("expected canceled lock wait")
	}
}
