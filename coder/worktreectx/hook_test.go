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
