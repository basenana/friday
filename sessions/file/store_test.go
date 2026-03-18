package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/core/types"
)

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
