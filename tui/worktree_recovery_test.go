package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basenana/friday/bus"
	coderloop "github.com/basenana/friday/coder/loop"
	"github.com/basenana/friday/core/actor/events"
	coresession "github.com/basenana/friday/core/session"
	fridayworktree "github.com/basenana/friday/worktree"
)

const worktreeLoopPhaseNamespace = "coder.loop.phase"

func TestWorktreeRuntimeSupervisorRecoversPersistedLoops(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	development, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := fixture.supervisor.Create(ctx, "restart review loop")
	if err != nil {
		t.Fatal(err)
	}
	if development.sessionID == review.sessionID {
		t.Fatal("worktrees share one session")
	}
	persistWorktreeLoop(t, development.lifecycle.Current(), coderloop.StateActive, "update")
	persistWorktreeLoop(t, review.lifecycle.Current(), coderloop.StateSuspended, "review")
	if err := fixture.supervisor.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := newWorktreeRuntimeSupervisor(fixture.service, fixture.sessions, fixture.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted supervisor: %v", err)
		}
	})
	inputs := make(chan bus.Envelope, 2)
	restarted.attachRuntime = func(ctx context.Context, runtime *worktreeRuntime) error {
		runtime.loop.Close()
		runtime.loop = coderloop.NewManager(runtime.bus, coderloop.WithInputDispatcher(func(env bus.Envelope) error {
			inputs <- env
			return nil
		}))
		return restarted.defaultAttachRuntime(ctx, runtime)
	}

	if err := restarted.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	wantPrompts := map[string]string{
		development.sessionID: coderloop.DevelopRecoveryPrompt,
		review.sessionID:      coderloop.ReviewRecoveryPrompt,
	}
	for range 2 {
		select {
		case env := <-inputs:
			var input bus.AgentTextInput
			if err := events.DecodePayload(env.Event, &input); err != nil {
				t.Fatal(err)
			}
			want, ok := wantPrompts[env.Session]
			if !ok {
				t.Fatalf("recovery dispatched for unexpected session %q", env.Session)
			}
			if input.Text != want {
				t.Fatalf("recovery prompt for %s = %q, want %q", env.Session, input.Text, want)
			}
			delete(wantPrompts, env.Session)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for recovery prompts; missing sessions: %#v", wantPrompts)
		}
	}
	if len(wantPrompts) != 0 {
		t.Fatalf("missing recovery prompts for sessions: %#v", wantPrompts)
	}
	if len(restarted.runtimes) != 2 {
		t.Fatalf("restored runtime count = %d, want 2", len(restarted.runtimes))
	}
	if restarted.active != nil {
		t.Fatal("startup recovery published a foreground before activation")
	}
	for _, original := range []*worktreeRuntime{development, review} {
		restored := restarted.runtimes[original.id]
		if restored == nil || restored.sessionID != original.sessionID {
			t.Fatalf("restored runtime %q = %#v, want session %q", original.id, restored, original.sessionID)
		}
		assertWorktreeLoopRecord(t, restored.lifecycle.Current(), coderloop.StateNamespace, string(coderloop.StateActive))
	}
}

func TestWorktreeRuntimeSupervisorRecoveryReplacesInvalidSessionWithoutLoopState(t *testing.T) {
	for _, invalidate := range []string{"missing", "archived"} {
		t.Run(invalidate, func(t *testing.T) {
			fixture := newWorktreeRuntimeTestFixture(t)
			ctx := context.Background()
			original, err := fixture.supervisor.Activate(ctx, fixture.mainID)
			if err != nil {
				t.Fatal(err)
			}
			persistWorktreeLoop(t, original.lifecycle.Current(), coderloop.StateActive, "review")
			oldSessionID := original.sessionID
			if err := fixture.supervisor.Close(); err != nil {
				t.Fatal(err)
			}
			switch invalidate {
			case "missing":
				if err := fixture.sessions.DeleteRoot(oldSessionID); err != nil {
					t.Fatal(err)
				}
			case "archived":
				if err := fixture.sessions.Archive(oldSessionID); err != nil {
					t.Fatal(err)
				}
			}

			stalePath := filepath.Join(t.TempDir(), "removed-checkout")
			stale, err := fixture.supervisor.store.Ensure(fridayworktree.Metadata{
				ID: "stale-checkout", Name: "stale-checkout", Path: stalePath, Branch: "friday/stale-checkout",
			})
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := newWorktreeRuntimeSupervisor(fixture.service, fixture.sessions, fixture.cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := restarted.Close(); err != nil {
					t.Errorf("close restarted supervisor: %v", err)
				}
			})
			attachCalls := 0
			restarted.attachRuntime = func(context.Context, *worktreeRuntime) error {
				attachCalls++
				return nil
			}

			if err := restarted.Restore(ctx); err != nil {
				t.Fatal(err)
			}
			restored := restarted.runtimes[fixture.mainID]
			if restored == nil {
				t.Fatal("current worktree runtime was not restored")
			}
			if restored.sessionID == oldSessionID {
				t.Fatal("invalid session reference was not replaced")
			}
			meta, err := restarted.store.Get(fixture.mainID)
			if err != nil {
				t.Fatal(err)
			}
			if meta.SessionID != restored.sessionID {
				t.Fatalf("persisted replacement session = %q, want %q", meta.SessionID, restored.sessionID)
			}
			if _, err := restored.lifecycle.Current().ReadRecord(ctx, coderloop.StateNamespace); !errors.Is(err, coresession.ErrRecordNotFound) {
				t.Fatalf("replacement inherited loop state: %v", err)
			}
			if attachCalls != 0 {
				t.Fatalf("replacement without loop state attached %d times", attachCalls)
			}
			if restarted.runtimes[stale.ID] != nil {
				t.Fatal("stale checkout received a runtime")
			}
			if len(restarted.runtimes) != 1 {
				t.Fatalf("restored runtime count = %d, want 1", len(restarted.runtimes))
			}
		})
	}
}

func TestWorktreeRuntimeSupervisorRecoverySkipsGitPrunableWorktree(t *testing.T) {
	fixture := newWorktreeRuntimeTestFixture(t)
	ctx := context.Background()
	valid, err := fixture.supervisor.Activate(ctx, fixture.mainID)
	if err != nil {
		t.Fatal(err)
	}
	prunable, err := fixture.supervisor.Create(ctx, "prunable recovery checkout")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.supervisor.Activate(ctx, valid.id); err != nil {
		t.Fatal(err)
	}
	if err := fixture.supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(prunable.workdir); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", valid.workdir, "worktree", "list", "--porcelain")
	porcelain, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list Git worktrees: %v: %s", err, porcelain)
	}
	if !strings.Contains(string(porcelain), "\nprunable ") {
		t.Fatalf("Git did not report the missing checkout as prunable:\n%s", porcelain)
	}

	restarted, err := newWorktreeRuntimeSupervisor(fixture.service, fixture.sessions, fixture.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted supervisor: %v", err)
		}
	})
	if err := restarted.Restore(ctx); err != nil {
		t.Fatalf("restore with prunable worktree: %v", err)
	}
	if restarted.runtimes[valid.id] == nil {
		t.Fatal("valid worktree runtime was not restored")
	}
	if restarted.runtimes[prunable.id] != nil {
		t.Fatal("prunable worktree received a runtime")
	}
	if len(restarted.runtimes) != 1 {
		t.Fatalf("restored runtime count = %d, want 1", len(restarted.runtimes))
	}
}

func persistWorktreeLoop(t *testing.T, sess *coresession.Session, state coderloop.State, phase string) {
	t.Helper()
	ctx := context.Background()
	if err := sess.UpdateRecord(ctx, coderloop.StateNamespace, func([]byte) ([]byte, error) {
		return []byte(state), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.UpdateRecord(ctx, worktreeLoopPhaseNamespace, func([]byte) ([]byte, error) {
		return []byte(phase), nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertWorktreeLoopRecord(t *testing.T, sess *coresession.Session, namespace, want string) {
	t.Helper()
	raw, err := sess.ReadRecord(context.Background(), namespace)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != want {
		t.Fatalf("loop record %q = %q, want %q", namespace, got, want)
	}
}
