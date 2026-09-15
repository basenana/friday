package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUserHistoryRoundTripAndConsecutiveDedup(t *testing.T) {
	store := NewFileStore(t.TempDir())
	projectID := "project-one"
	when := time.Date(2026, 9, 15, 10, 20, 30, 0, time.UTC)
	first := UserHistoryEntry{Version: 1, Text: "first", CreatedAt: when, SessionID: "session-a"}

	appended, err := store.AppendUserHistory(projectID, first)
	if err != nil || !appended {
		t.Fatalf("append first = %v, %v", appended, err)
	}
	if appended, err = store.AppendUserHistory(projectID, first); err != nil || appended {
		t.Fatalf("append duplicate = %v, %v", appended, err)
	}
	second := UserHistoryEntry{Version: 1, Text: "second", CreatedAt: when.Add(time.Second), SessionID: "session-b"}
	if appended, err = store.AppendUserHistory(projectID, second); err != nil || !appended {
		t.Fatalf("append second = %v, %v", appended, err)
	}
	if appended, err = store.AppendUserHistory(projectID, first); err != nil || !appended {
		t.Fatalf("append non-consecutive duplicate = %v, %v", appended, err)
	}

	got, err := store.LoadUserHistory(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != first || got[1] != second || got[2] != first {
		t.Fatalf("history = %#v", got)
	}
	info, err := os.Stat(store.userHistoryPath(projectID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("history permissions = %o", info.Mode().Perm())
	}
}

func TestUserHistoryIsProjectScopedAndToleratesBadRecords(t *testing.T) {
	base := t.TempDir()
	store := NewFileStore(base)
	entry := UserHistoryEntry{Version: 1, Text: "kept", CreatedAt: time.Now(), SessionID: "session-a"}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	path := store.userHistoryPath("project-a")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := append(encoded, '\n')
	data = append(data, []byte("not-json\n")...)
	data = append(data, []byte(`{"version":1,"text":"truncated"`)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadUserHistory("project-a")
	if err != nil || len(got) != 1 || got[0].Text != "kept" {
		t.Fatalf("project-a history = %#v, %v", got, err)
	}
	after := UserHistoryEntry{Version: 1, Text: "after repair", CreatedAt: time.Now(), SessionID: "session-b"}
	if appended, err := store.AppendUserHistory("project-a", after); err != nil || !appended {
		t.Fatalf("append after truncated tail = %v, %v", appended, err)
	}
	got, err = store.LoadUserHistory("project-a")
	if err != nil || len(got) != 2 || got[1].Text != "after repair" {
		t.Fatalf("repaired history = %#v, %v", got, err)
	}
	other, err := store.LoadUserHistory("project-b")
	if err != nil || len(other) != 0 {
		t.Fatalf("project-b history = %#v, %v", other, err)
	}
}

func TestUserHistoryConcurrentAppends(t *testing.T) {
	base := t.TempDir()
	stores := []*FileStore{NewFileStore(base), NewFileStore(base)}
	const count = 20
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := UserHistoryEntry{Version: 1, Text: fmt.Sprintf("prompt-%02d", i), CreatedAt: time.Now(), SessionID: "session"}
			if _, err := stores[i%len(stores)].AppendUserHistory("project", entry); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := stores[0].LoadUserHistory("project")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != count {
		t.Fatalf("history length = %d, want %d", len(got), count)
	}
}
