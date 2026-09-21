package codebase

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/basenana/friday/sandbox"
)

func TestCollectGitStateAndCoverage(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = []string{"git *"}
	executor := sandbox.NewExecutor(cfg)
	state, err := collectGitState(context.Background(), executor, root, 1, Status{})
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsGit || state.HEAD == "" || state.Dirty || !reflect.DeepEqual(state.Candidates, []string{"a.txt"}) || state.AfterPath != "a.txt" || state.Total != 2 {
		t.Fatalf("state = %+v", state)
	}

	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "c.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty, err := collectGitState(context.Background(), executor, root, 10, Status{})
	if err != nil {
		t.Fatal(err)
	}
	if !dirty.Dirty || dirty.DirtyHash == "" || !reflect.DeepEqual(dirty.Changed, []string{"a.txt", "c.txt"}) {
		t.Fatalf("dirty = %+v", dirty)
	}

	advanced := advanceCoverage(Status{}, dirty)
	if advanced.AfterPath != dirty.AfterPath || advanced.PresentedCount != len(dirty.Candidates) {
		t.Fatalf("advanced = %+v", advanced)
	}
}

func TestCoverageStopsAfterCandidateSetIsComplete(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "init")

	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = []string{"git *"}
	executor := sandbox.NewExecutor(cfg)
	status := Status{}
	for _, want := range []string{"a.txt", "b.txt"} {
		state, err := collectGitState(context.Background(), executor, root, 1, status)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.Candidates, []string{want}) {
			t.Fatalf("candidates=%v, want [%s]", state.Candidates, want)
		}
		status = advanceCoverage(status, state)
	}
	state, err := collectGitState(context.Background(), executor, root, 1, status)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Candidates) != 0 {
		t.Fatalf("completed coverage restarted with %v", state.Candidates)
	}
	status = advanceCoverage(status, state)
	if status.PresentedCount != status.CandidateCount {
		t.Fatalf("presented=%d candidates=%d", status.PresentedCount, status.CandidateCount)
	}
}

func TestDirtyHashIncludesStagedState(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = []string{"git *"}
	executor := sandbox.NewExecutor(cfg)
	if err := os.WriteFile(path, []byte("staged-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "file.txt")
	if err := os.WriteFile(path, []byte("same-worktree"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := collectGitState(context.Background(), executor, root, 10, Status{})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("staged-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "file.txt")
	if err := os.WriteFile(path, []byte("same-worktree"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := collectGitState(context.Background(), executor, root, 10, Status{})
	if err != nil {
		t.Fatal(err)
	}
	if first.DirtyHash == second.DirtyHash {
		t.Fatalf("dirty hash ignored staged content: %s", first.DirtyHash)
	}
}

func TestCollectGitStateUnbornRepository(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("untracked"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "staged.txt")

	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = []string{"git *"}
	state, err := collectGitState(context.Background(), sandbox.NewExecutor(cfg), root, 10, Status{})
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsGit || state.HEAD != "" || !state.Dirty {
		t.Fatalf("state=%+v", state)
	}
	if !reflect.DeepEqual(state.Changed, []string{"staged.txt", "untracked.txt"}) {
		t.Fatalf("changed=%v", state.Changed)
	}
}

func TestObserveGitStateDoesNotAdvanceIndexedWatermark(t *testing.T) {
	status := Status{IndexedHEAD: "old"}
	observed := observeGitState(status, gitState{HEAD: "new", Branch: "main", Detached: false, Dirty: true, DirtyHash: "dirty"})
	if observed.ObservedHEAD != "new" || observed.IndexedHEAD != "old" || observed.Branch != "main" || !observed.Dirty || observed.DirtyHash != "dirty" {
		t.Fatalf("observed=%+v", observed)
	}
}

func TestCollectGitStateNonGit(t *testing.T) {
	cfg := sandbox.DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = []string{"git *"}
	state, err := collectGitState(context.Background(), sandbox.NewExecutor(cfg), t.TempDir(), 5, Status{})
	if err != nil {
		t.Fatal(err)
	}
	if state.IsGit || len(state.Candidates) != 0 {
		t.Fatalf("state = %+v", state)
	}
}
