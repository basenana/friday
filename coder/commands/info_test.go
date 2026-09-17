package commands

import (
	"path/filepath"
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

func TestResumeCmdCarriesTargetWithoutCreatingSession(t *testing.T) {
	mgr := newSessionManagerForTest(t)
	cmd := resumeCmd{}

	result, err := cmd.Execute(&Context{
		SessMgr: mgr,
		Args:    []string{"missing-session"},
		RawArgs: "missing-session",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if target := actionAt[ResumeSessionAction](t, result, 0).Target; target != "missing-session" {
		t.Fatalf("resume target = %q", target)
	}

	ok, err := mgr.Exists("missing-session")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if ok {
		t.Fatal("missing session should not have been created")
	}
}

func TestEffortCommandOpensSelectorOrSetsValue(t *testing.T) {
	result, err := (effortCmd{}).Execute(&Context{})
	if err != nil {
		t.Fatal(err)
	}
	actionAt[OpenEffortAction](t, result, 0)

	result, err = (effortCmd{}).Execute(&Context{Args: []string{"HIGH"}, RawArgs: "HIGH"})
	if err != nil {
		t.Fatal(err)
	}
	if got := actionAt[SetEffortAction](t, result, 0).Effort; got != "high" {
		t.Fatalf("effort = %q, want high", got)
	}
	if _, err := (effortCmd{}).Execute(&Context{Args: []string{"turbo"}}); err == nil {
		t.Fatal("invalid effort was accepted")
	}
}
