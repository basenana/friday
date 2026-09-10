package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/core/contextmgr"
	"github.com/basenana/friday/core/types"
)

func TestEventStoreRoundTripAndRepairsTruncatedTail(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	if _, err := store.Create("events", nil); err != nil {
		t.Fatal(err)
	}
	sink, err := store.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	want := events.NewEvent(events.KindRunStarted, "run-1")
	if err := sink.Append(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	path := store.eventsPath("events")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":`)
	_ = f.Close()
	repaired, err := store.OpenEventSink(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	_ = repaired.Close()
	got, err := store.LoadEvents(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != want.RunID {
		t.Fatalf("events = %#v", got)
	}
}

func TestEventStoreCompactsLargeLogsAndKeepsLatestRun(t *testing.T) {
	dir := t.TempDir()
	store := NewFileSessionStore(dir)
	if _, err := store.Create("bounded", nil); err != nil {
		t.Fatal(err)
	}
	sink, err := store.OpenEventSink(context.Background(), "bounded")
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 4096)
	for run := 0; run < 24; run++ {
		runID := fmt.Sprintf("run-%d", run)
		if err := sink.Append(context.Background(), events.NewEvent(events.KindRunStarted, runID)); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			evt := events.NewEvent(events.KindTextMessageContent, runID).WithPayload(events.TextMessageContentData{Content: payload})
			if err := sink.Append(context.Background(), evt); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.Append(context.Background(), events.NewEvent(events.KindRunFinished, runID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.eventsPath("bounded"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxEventLogBytes {
		t.Fatalf("event log size = %d, max = %d", info.Size(), maxEventLogBytes)
	}
	got, err := store.LoadEvents(context.Background(), "bounded")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1].RunID != "run-23" || got[len(got)-1].Type != events.KindRunFinished {
		t.Fatalf("latest event not retained: %#v", got[len(got)-1])
	}
}

func TestReplaceMessages(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "session_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewFileSessionStore(tmpDir)
	if err := store.EnsureDir(); err != nil {
		t.Fatalf("failed to ensure dir: %v", err)
	}

	sessionID := "test-session-001"

	// 1. Create session
	sess, err := store.Create(sessionID, nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// 2. Add some messages
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi there!"},
		{Role: types.RoleUser, Content: "How are you?"},
		{Role: types.RoleAssistant, Content: "I'm doing well, thanks!"},
	}
	if err := store.AppendMessages(sessionID, msgs...); err != nil {
		t.Fatalf("failed to append messages: %v", err)
	}

	// Verify message count
	loaded, err := store.LoadMessages(sessionID)
	if err != nil {
		t.Fatalf("failed to load messages: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(loaded))
	}

	// 3. Call ReplaceMessages (simulate compact)
	compactMsgs := []types.Message{
		{Role: types.RoleSystem, Content: "Summary of previous conversation..."},
		{Role: types.RoleUser, Content: "Hello"},
		{Role: types.RoleAssistant, Content: "Hi there!"},
	}
	if err := store.ReplaceMessages(sessionID, compactMsgs...); err != nil {
		t.Fatalf("failed to replace messages: %v", err)
	}

	// 4. Verify backup file exists
	files, err := os.ReadDir(filepath.Join(tmpDir, sessionID))
	if err != nil {
		t.Fatalf("failed to read session dir: %v", err)
	}

	var hasBackup bool
	var hasHistory bool
	for _, f := range files {
		if f.Name() == "history.jsonl" {
			hasHistory = true
		}
		if len(f.Name()) > len("history_origin_") && f.Name()[:len("history_origin_")] == "history_origin_" {
			hasBackup = true
			t.Logf("Found backup file: %s", f.Name())
		}
	}

	if !hasBackup {
		t.Error("expected backup file to exist")
	}
	if !hasHistory {
		t.Error("expected history.jsonl to exist")
	}

	// 5. Verify new history.jsonl content
	newLoaded, err := store.LoadMessages(sessionID)
	if err != nil {
		t.Fatalf("failed to load new messages: %v", err)
	}
	if len(newLoaded) != 3 {
		t.Fatalf("expected 3 messages after compact, got %d", len(newLoaded))
	}

	// 6. Verify first message is system summary
	if newLoaded[0].Role != types.RoleSystem {
		t.Errorf("expected first message to be system role, got %s", newLoaded[0].Role)
	}

	t.Logf("Session ID: %s", sess.ID)
	t.Logf("Original messages: %d, After compact: %d", len(loaded), len(newLoaded))
}

func TestSessionMemoryRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session_memory_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewFileSessionStore(tmpDir)
	if err := store.EnsureDir(); err != nil {
		t.Fatalf("failed to ensure dir: %v", err)
	}

	sessionID := "test-session-memory-001"
	if _, err := store.Create(sessionID, nil); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	record := &contextmgr.SessionMemoryRecord{
		GeneratedAt:    time.Now().Add(-time.Minute).UTC().Truncate(time.Second),
		LastSyncAt:     time.Now().UTC().Truncate(time.Second),
		TaskObjective:  "persist session memory",
		CurrentStatus:  "wrote session memory to disk",
		KeyDecisions:   []string{"use session_memory.json"},
		RecentWork:     []string{"added file store round-trip test"},
		PendingItems:   []string{"wire store into setup"},
		FileReferences: []string{"sessions/file/store.go"},
		ImportantCtx:   "stored alongside history.jsonl",
	}

	if err := store.WriteSessionMemory(sessionID, record); err != nil {
		t.Fatalf("WriteSessionMemory failed: %v", err)
	}

	loaded, err := store.ReadSessionMemory(sessionID)
	if err != nil {
		t.Fatalf("ReadSessionMemory failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected persisted session memory record")
	}
	if loaded.TaskObjective != record.TaskObjective {
		t.Fatalf("expected task objective %q, got %q", record.TaskObjective, loaded.TaskObjective)
	}
	if len(loaded.FileReferences) != 1 || loaded.FileReferences[0] != "sessions/file/store.go" {
		t.Fatalf("unexpected file references: %#v", loaded.FileReferences)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, sessionID, "session_memory.json")); err != nil {
		t.Fatalf("expected session_memory.json to exist: %v", err)
	}
}

func TestCalibratedMessageTokensPersistAcrossReload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session_tokens_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewFileSessionStore(tmpDir)
	if err := store.EnsureDir(); err != nil {
		t.Fatalf("failed to ensure dir: %v", err)
	}

	sessionID := "test-session-tokens-001"
	sess, err := store.Create(sessionID, nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	sess.AppendMessage(&types.Message{Role: types.RoleUser, Content: "Hello world", Tokens: 42})

	reloaded, err := store.Load(sessionID, nil)
	if err != nil {
		t.Fatalf("failed to reload session: %v", err)
	}

	history := reloaded.GetHistory()
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Tokens != 42 {
		t.Fatalf("expected persisted calibrated tokens=42, got %d", history[0].Tokens)
	}
}
