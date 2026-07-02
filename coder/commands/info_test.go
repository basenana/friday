package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

func newSessionManagerForTest(t *testing.T) *sessions.Manager {
	t.Helper()
	baseDir := t.TempDir()
	store := sessionfile.NewFileSessionStore(filepath.Join(baseDir, "sessions"))
	return sessions.NewManager(store, filepath.Join(baseDir, "current"), "test")
}

func TestSessionCmdUseMissingDoesNotCreateSession(t *testing.T) {
	mgr := newSessionManagerForTest(t)
	cmd := sessionCmd{}

	result, err := cmd.Execute(&Context{
		SessMgr: mgr,
		Args:    []string{"use", "missing-session"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.SwitchSession != "" {
		t.Fatalf("SwitchSession = %q, want empty", result.SwitchSession)
	}
	if !strings.Contains(result.Message, "session not found") {
		t.Fatalf("message = %q, want session not found", result.Message)
	}

	ok, err := mgr.Exists("missing-session")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if ok {
		t.Fatal("missing session should not have been created")
	}
}
