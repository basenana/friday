package worktreectx

import (
	"context"
	"strings"
	"testing"

	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
)

func TestHookInjectsImmutableWorktreeContextWithoutPersistingHistory(t *testing.T) {
	info := Context{
		ProjectName: "juicefs-csi-driver", ProjectRoot: "/repo",
		WorktreeName: "fix-mount", Branch: "friday/fix-mount",
		WorktreeRoot: "/repo/.worktrees/fix-mount", SessionID: "session-a",
	}
	hook, err := New(info)
	if err != nil {
		t.Fatal(err)
	}
	sess := coresession.New("session-a", nil)
	req := providers.NewRequest("")
	if err := hook.BeforeModel(context.Background(), sess, req); err != nil {
		t.Fatal(err)
	}
	if len(sess.GetHistory()) != 0 {
		t.Fatalf("hook persisted history: %#v", sess.GetHistory())
	}
	if len(req.History()) != 1 {
		t.Fatalf("request history = %#v", req.History())
	}
	if got := hook.ReservedTokens(sess); got <= 0 {
		t.Fatalf("ReservedTokens() = %d, want positive budget", got)
	}
	content := req.History()[0].Content
	for _, want := range []string{
		"juicefs-csi-driver", "/repo", "fix-mount", "friday/fix-mount",
		"/repo/.worktrees/fix-mount", "session-a",
		"Relative filesystem and shell paths resolve from the active worktree",
		"The full project code root is writable",
		"Default to modifying only the active worktree",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("context missing %q:\n%s", want, content)
		}
	}
}

func TestNewRejectsIncompleteWorktreeContext(t *testing.T) {
	if _, err := New(Context{}); err == nil {
		t.Fatal("expected incomplete context to fail")
	}
}

func TestHookRendersTheLiveSessionID(t *testing.T) {
	hook, err := New(Context{
		ProjectName: "repo", ProjectRoot: "/repo",
		WorktreeName: "main", Branch: "main",
		WorktreeRoot: "/repo", SessionID: "build-time-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := providers.NewRequest("")
	if err := hook.BeforeModel(context.Background(), coresession.New("switched-session", nil), req); err != nil {
		t.Fatal(err)
	}
	content := req.History()[0].Content
	if !strings.Contains(content, "Worktree session: switched-session") {
		t.Fatalf("context kept the stale session:\n%s", content)
	}
	if strings.Contains(content, "build-time-session") {
		t.Fatalf("context still contains the build-time session:\n%s", content)
	}

	// A request without a live session keeps the build-time binding.
	fallback := providers.NewRequest("")
	if err := hook.BeforeModel(context.Background(), nil, fallback); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fallback.History()[0].Content, "Worktree session: build-time-session") {
		t.Fatalf("fallback context lost the session binding:\n%s", fallback.History()[0].Content)
	}
}
