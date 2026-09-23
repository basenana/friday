package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
	fridayworktree "github.com/basenana/friday/worktree"
	"github.com/spf13/pflag"
)

func TestWorktreesListPrintsDeterministicProjectStateWithoutConversationContent(t *testing.T) {
	repo := newCommandGitRepo(t)
	cfg := commandWorktreeConfig(t)
	service, err := fridayworktree.Open(context.Background(), repo, cfg.WorktreePath(), cfg.Worktree.BranchPrefix, cfg.DataDirPath())
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := sessionfile.NewFileSessionStore(cfg.SessionsPath())
	manager := sessions.NewManager(sessionStore, filepath.Join(cfg.DataDirPath(), "current"), "")
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	healthyID := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sessionStore.AppendMessages(healthyID, types.Message{Role: types.RoleUser, Content: "TOP SECRET conversation content"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Associate(repo, "main", service.RepositoryID(), healthyID); err != nil {
		t.Fatal(err)
	}
	linked, err := service.Create(context.Background(), "linked checkout")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Associate(linked.Worktree.Path, linked.Worktree.Branch, service.RepositoryID(), "missing-session"); err != nil {
		t.Fatal(err)
	}
	stale, err := service.Create(context.Background(), "stale checkout")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Associate(stale.Worktree.Path, stale.Worktree.Branch, service.RepositoryID(), ""); err != nil {
		t.Fatal(err)
	}
	runCommandGit(t, repo, "worktree", "remove", stale.Worktree.Path)

	metadata, err := fridayworktree.NewStore(cfg.ProjectsPath(), service.RepositoryID())
	if err != nil {
		t.Fatal(err)
	}
	items, err := metadata.List()
	if err != nil {
		t.Fatal(err)
	}
	type expectedRow struct {
		id, name, branch, path, health, current, stale string
	}
	rows := make([]expectedRow, 0, len(items))
	for _, item := range items {
		health, current, staleMarker := "none", "-", "-"
		if item.SessionID == healthyID {
			health = "healthy"
		} else if item.SessionID != "" {
			health = "missing"
		}
		resolvedRepo, _ := filepath.EvalSymlinks(repo)
		if item.Path == resolvedRepo {
			current = "yes"
		}
		if item.Path == stale.Worktree.Path {
			staleMarker = "yes"
		}
		rows = append(rows, expectedRow{item.ID, item.Name, item.Branch, item.Path, health, current, staleMarker})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	want := "ID\tNAME\tBRANCH\tPATH\tSESSION\tCURRENT\tSTALE\n"
	for _, row := range rows {
		want += fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.id, row.name, row.branch, row.path, row.health, row.current, row.stale)
	}

	var first, second bytes.Buffer
	if err := runWorktreesList(context.Background(), repo, cfg, manager, &first); err != nil {
		t.Fatal(err)
	}
	if err := runWorktreesList(context.Background(), repo, cfg, manager, &second); err != nil {
		t.Fatal(err)
	}
	if got := first.String(); got != want {
		t.Fatalf("worktrees list =\n%s\nwant:\n%s", got, want)
	}
	if second.String() != first.String() {
		t.Fatalf("worktrees list changed between calls:\nfirst=%q\nsecond=%q", first.String(), second.String())
	}
	if strings.Contains(first.String(), "TOP SECRET") {
		t.Fatalf("worktrees list exposed conversation content: %q", first.String())
	}
}

func TestWorktreesRemoveCommandRemovesExactlyOneProjectEntry(t *testing.T) {
	repo := newCommandGitRepo(t)
	cfg := commandWorktreeConfig(t)
	service, err := fridayworktree.Open(context.Background(), repo, cfg.WorktreePath(), cfg.Worktree.BranchPrefix, cfg.DataDirPath())
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), "command removal")
	if err != nil {
		t.Fatal(err)
	}
	store, err := fridayworktree.NewStore(cfg.ProjectsPath(), service.RepositoryID())
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.Get(created.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}
	manager := sessions.NewManager(sessionfile.NewFileSessionStore(cfg.SessionsPath()), filepath.Join(cfg.DataDirPath(), "current"), "")

	if err := runWorktreesRemove(context.Background(), repo, cfg, manager, meta.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(meta.ID); err == nil {
		t.Fatal("worktrees remove left metadata")
	}
	if _, err := os.Stat(created.Worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktrees remove left checkout: %v", err)
	}
	if branch := strings.TrimSpace(commandGitOutput(t, repo, "branch", "--list", created.Worktree.Branch)); branch == "" {
		t.Fatal("worktrees remove deleted branch without --delete-branch")
	}
}

func TestWorktreesRemoveRequiresOneIDAndOnlyDefinesDeleteBranch(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"worktrees", "remove"})
	if err != nil || command == nil || command.Name() != "remove" {
		t.Fatalf("worktrees remove command = %v, %v", command, err)
	}
	for _, args := range [][]string{nil, {"one", "two"}} {
		if err := command.Args(command, args); err == nil {
			t.Fatalf("worktrees remove accepted args %#v", args)
		}
	}
	if err := command.Args(command, []string{"one"}); err != nil {
		t.Fatalf("worktrees remove rejected one ID: %v", err)
	}
	var localFlags []string
	command.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" {
			return
		}
		localFlags = append(localFlags, flag.Name)
	})
	if strings.Join(localFlags, ",") != "delete-branch" {
		t.Fatalf("worktrees remove flags = %v, want only delete-branch", localFlags)
	}
	if singular, _, err := rootCmd.Find([]string{"worktree"}); err == nil && singular != nil && singular.Name() == "worktree" {
		t.Fatal("singular worktree command remains registered")
	}
}
