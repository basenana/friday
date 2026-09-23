//go:build e2e

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	codebasepkg "github.com/basenana/friday/coder/codebase"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/utils/logger"
	fridayworktree "github.com/basenana/friday/worktree"
)

func TestProjectWorktreeCommandsLifecycle(t *testing.T) {
	runProjectWorktreeCommandsLifecycleE2E(t)
}

func runProjectWorktreeCommandsLifecycleE2E(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	repo := newCommandGitRepo(t)
	t.Chdir(repo)
	t.Setenv("FRIDAY_TTY", "task10-e2e")
	cfg := commandWorktreeConfig(t)
	cfg.Memory.Enabled = false
	configPath := filepath.Join(t.TempDir(), "friday.json")
	if _, err := config.WriteConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	service, err := fridayworktree.Open(ctx, repo, cfg.WorktreePath(), cfg.Worktree.BranchPrefix, cfg.DataDirPath())
	if err != nil {
		t.Fatal(err)
	}
	store, err := fridayworktree.NewStore(cfg.ProjectsPath(), service.ProjectIdentity().ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := sessionfile.NewFileSessionStore(cfg.SessionsPath())
	manager := sessions.NewManager(
		sessionStore,
		filepath.Join(cfg.DataDirPath(), "current-task10-e2e"),
		"task10-e2e",
	)

	createdA, err := service.Create(ctx, "command lifecycle A")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = createdA.Rollback(context.Background()) })
	createdB, err := service.Create(ctx, "command lifecycle B")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = createdB.Rollback(context.Background()) })
	metaA, err := store.Get(createdA.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}
	metaB, err := store.Get(createdB.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}
	sessionA := createCommandLifecycleRoot(t, manager)
	sessionB := createCommandLifecycleRoot(t, manager)
	if err := service.Associate(metaA.Path, metaA.Branch, service.ProjectIdentity().ID, sessionA); err != nil {
		t.Fatal(err)
	}
	if err := service.Associate(metaB.Path, metaB.Branch, service.ProjectIdentity().ID, sessionB); err != nil {
		t.Fatal(err)
	}
	if err := sessionStore.AppendMessages(sessionB, types.Message{Role: types.RoleUser, Content: "TOP SECRET lifecycle content"}); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(cfg.DataDirPath(), "worktrees", service.RepositoryID(), "registry.json")
	writeCommandLegacyRegistry(t, legacyPath, repo, "main", "legacy-main-session", time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC))
	installCommandLifecycleGlobalsCleanup(t)
	listOutput, err := executeCommandLifecycleCobra(t, configPath, "worktrees", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ID\tNAME\tBRANCH\tPATH\tSESSION\tCURRENT\tSTALE",
		metaA.ID + "\t", metaB.ID + "\t", "\thealthy\t", "\tmissing\tyes\t-",
	} {
		if !strings.Contains(listOutput, want) {
			t.Fatalf("worktrees list missing %q:\n%s", want, listOutput)
		}
	}
	if strings.Contains(listOutput, "TOP SECRET") {
		t.Fatalf("worktrees list exposed conversation content: %q", listOutput)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	migratedMain, err := store.Get(canonicalRepo)
	if err != nil {
		t.Fatalf("list entrypoint did not migrate legacy metadata: %v", err)
	}
	if migratedMain.SessionID != "legacy-main-session" {
		t.Fatalf("migrated main session = %q", migratedMain.SessionID)
	}

	releaseOwner, err := codebasepkg.AcquireProjectLock(cfg.DataDirPath(), service.ProjectIdentity().ID, repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(releaseOwner)
	if _, err := executeCommandLifecycleCobra(t, configPath, "worktrees", "remove", metaB.ID); err == nil || !strings.Contains(err.Error(), "running project TUI owns") {
		t.Fatalf("remove while TUI lock is held error = %v", err)
	}
	if _, err := os.Stat(metaB.Path); err != nil {
		t.Fatalf("lock refusal changed B checkout: %v", err)
	}
	releaseOwner()

	if _, err := executeCommandLifecycleCobra(t, configPath, "worktrees", "remove", metaB.ID); err != nil {
		t.Fatal(err)
	}
	assertCommandLifecycleArchived(t, manager, sessionB)
	if _, err := os.Stat(metaB.Path); !os.IsNotExist(err) {
		t.Fatalf("remove B left checkout: %v", err)
	}
	if branch := strings.TrimSpace(commandGitOutput(t, repo, "branch", "--list", metaB.Branch)); branch == "" {
		t.Fatal("remove B deleted its branch without --delete-branch")
	}

	if _, err := executeCommandLifecycleCobra(t, configPath, "worktrees", "remove", "--delete-branch", metaA.ID); err != nil {
		t.Fatal(err)
	}
	assertCommandLifecycleArchived(t, manager, sessionA)
	if _, err := os.Stat(metaA.Path); !os.IsNotExist(err) {
		t.Fatalf("remove A left checkout: %v", err)
	}
	if branch := strings.TrimSpace(commandGitOutput(t, repo, "branch", "--list", metaA.Branch)); branch != "" {
		t.Fatalf("remove A left safely deletable branch: %q", branch)
	}

	finalList, err := executeCommandLifecycleCobra(t, configPath, "worktrees", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(finalList, metaA.ID) || strings.Contains(finalList, metaB.ID) {
		t.Fatalf("removed worktrees remain in command output:\n%s", finalList)
	}
}

func createCommandLifecycleRoot(t *testing.T, manager *sessions.Manager) string {
	t.Helper()
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lifecycle.Close() })
	id := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

func executeCommandLifecycleCobra(t *testing.T, configPath string, args ...string) (string, error) {
	t.Helper()
	logger.InitWithFile(filepath.Join(t.TempDir(), "friday.log"))
	defer logger.Close()
	var output bytes.Buffer
	previousOut, previousErr := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr()
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)
	defer rootCmd.SetOut(previousOut)
	defer rootCmd.SetErr(previousErr)
	rootCmd.SetArgs(append([]string{"--config", configPath}, args...))
	_, err := rootCmd.ExecuteC()
	return output.String(), err
}

func installCommandLifecycleGlobalsCleanup(t *testing.T) {
	t.Helper()
	previousCfgFile, previousWorkspace := cfgFile, workspaceDir
	previousCfg, previousSessions, previousTTY := cfg, sessMgr, currentTTY
	previousDeleteBranch := worktreesRemoveDeleteBranch
	if flag := worktreesRemoveCmd.Flags().Lookup("delete-branch"); flag != nil {
		_ = flag.Value.Set("false")
		flag.Changed = false
	}
	worktreesRemoveDeleteBranch = false
	t.Cleanup(func() {
		cfgFile, workspaceDir = previousCfgFile, previousWorkspace
		cfg, sessMgr, currentTTY = previousCfg, previousSessions, previousTTY
		worktreesRemoveDeleteBranch = previousDeleteBranch
		if flag := worktreesRemoveCmd.Flags().Lookup("delete-branch"); flag != nil {
			_ = flag.Value.Set("false")
			flag.Changed = false
		}
		rootCmd.SetArgs(nil)
	})
}

func assertCommandLifecycleArchived(t *testing.T, manager *sessions.Manager, sessionID string) {
	t.Helper()
	if exists, err := manager.Exists(sessionID); err != nil || !exists {
		t.Fatalf("archived session %q exists = %t, err = %v", sessionID, exists, err)
	}
	if active, err := manager.IsActive(sessionID); err != nil || active {
		t.Fatalf("archived session %q active = %t, err = %v", sessionID, active, err)
	}
}
