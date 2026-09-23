package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	fridayworktree "github.com/basenana/friday/worktree"
)

func TestSingularWorktreeCommandRemoved(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"worktree"})
	if err == nil && command != nil && command.Name() == "worktree" {
		t.Fatal("singular worktree command remains registered")
	}
}

func TestTUIRuntimeDispatch(t *testing.T) {
	t.Run("Git uses worktree runtime", func(t *testing.T) {
		repo := newCommandGitRepo(t)
		cfg := commandWorktreeConfig(t)
		var projectCalls, worktreeCalls int
		err := runTUI(context.Background(), repo, "", nil, cfg,
			func(*projectpkg.Manager, *config.Config, string) error {
				projectCalls++
				return nil
			},
			func(_ *config.Config, cwd string) error {
				worktreeCalls++
				if cwd != repo {
					t.Fatalf("worktree cwd = %q, want %q", cwd, repo)
				}
				return nil
			})
		if err != nil || projectCalls != 0 || worktreeCalls != 1 {
			t.Fatalf("dispatch error=%v project=%d worktree=%d", err, projectCalls, worktreeCalls)
		}
	})

	t.Run("non-Git preserves project session", func(t *testing.T) {
		root := t.TempDir()
		cfg := commandWorktreeConfig(t)
		var gotSession string
		err := runTUI(context.Background(), root, "session-1", nil, cfg,
			func(_ *projectpkg.Manager, _ *config.Config, sessionID string) error {
				gotSession = sessionID
				return nil
			},
			func(*config.Config, string) error {
				t.Fatal("non-Git directory launched worktree runtime")
				return nil
			})
		if err != nil || gotSession != "session-1" {
			t.Fatalf("dispatch error=%v session=%q", err, gotSession)
		}
	})

	t.Run("Git rejects session flag", func(t *testing.T) {
		repo := newCommandGitRepo(t)
		err := runTUI(context.Background(), repo, "session-1", nil, commandWorktreeConfig(t),
			func(*projectpkg.Manager, *config.Config, string) error { return nil },
			func(*config.Config, string) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "--session") {
			t.Fatalf("error = %v, want --session guidance", err)
		}
	})
}

func TestClassifyProjectDirectory(t *testing.T) {
	ctx := context.Background()
	repo := newCommandGitRepo(t)
	nested := filepath.Join(repo, "nested", "directory")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{repo, nested} {
		gitProject, err := classifyProjectDirectory(ctx, path)
		if err != nil || !gitProject {
			t.Fatalf("classify %q = %v, %v; want Git", path, gitProject, err)
		}
	}

	linked := filepath.Join(t.TempDir(), "linked")
	runCommandGit(t, repo, "worktree", "add", "-b", "classify-linked", linked, "HEAD")
	t.Cleanup(func() { _ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", linked).Run() })
	gitProject, err := classifyProjectDirectory(ctx, linked)
	if err != nil || !gitProject {
		t.Fatalf("classify linked worktree = %v, %v; want Git", gitProject, err)
	}

	plain := t.TempDir()
	gitProject, err = classifyProjectDirectory(ctx, plain)
	if err != nil || gitProject {
		t.Fatalf("classify plain directory = %v, %v; want non-Git", gitProject, err)
	}

	malformed := t.TempDir()
	if err := os.WriteFile(filepath.Join(malformed, ".git"), []byte("not a gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := classifyProjectDirectory(ctx, malformed); err == nil {
		t.Fatal("malformed Git marker was treated as a non-Git directory")
	}

	dangling := t.TempDir()
	if err := os.Symlink(filepath.Join(dangling, "missing-gitdir"), filepath.Join(dangling, ".git")); err != nil {
		t.Fatal(err)
	}
	if gitProject, err := classifyProjectDirectory(ctx, dangling); err == nil || gitProject {
		t.Fatalf("classify dangling Git marker = %v, %v; want discovery error", gitProject, err)
	}
}

func TestWorktreeCommandMigratesLegacyRegistry(t *testing.T) {
	repo := newCommandGitRepo(t)
	cfg := commandWorktreeConfig(t)
	service, err := fridayworktree.Open(context.Background(), repo, cfg.WorktreePath(), cfg.Worktree.BranchPrefix, cfg.DataDirPath())
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(cfg.DataDirPath(), "worktrees", service.RepositoryID(), "registry.json")
	created := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	writeCommandLegacyRegistry(t, legacyPath, repo, "main", "existing-session", created)

	proj, err := prepareLogicalProject(context.Background(), repo, cfg)
	if err != nil {
		t.Fatalf("prepareLogicalProject: %v", err)
	}
	if proj.ID() != service.ProjectIdentity().ID {
		t.Fatalf("project ID = %q, want %q", proj.ID(), service.ProjectIdentity().ID)
	}
	store, err := fridayworktree.NewStore(cfg.ProjectsPath(), proj.ID())
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SessionID != "existing-session" {
		t.Fatalf("migrated worktrees = %#v", items)
	}
}

func TestWorktreeCommandMigratesRawLegacyRepositoryID(t *testing.T) {
	for _, repositoryName := range []string{"my repo", `my\repo`} {
		t.Run(repositoryName, func(t *testing.T) {
			repo := newCommandGitRepoAt(t, filepath.Join(t.TempDir(), repositoryName))
			cfg := commandWorktreeConfig(t)
			common := strings.TrimSpace(commandGitOutput(t, repo, "rev-parse", "--git-common-dir"))
			if !filepath.IsAbs(common) {
				common = filepath.Join(repo, common)
			}
			common, err := filepath.EvalSymlinks(common)
			if err != nil {
				t.Fatal(err)
			}
			legacyID := rawLegacyRepositoryID(common)
			legacyPath := filepath.Join(cfg.DataDirPath(), "worktrees", legacyID, "registry.json")
			writeCommandLegacyRegistry(t, legacyPath, repo, "main", "legacy-session", time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC))

			proj, err := prepareLogicalProject(context.Background(), repo, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if proj.ID() == legacyID {
				t.Fatalf("test repository did not produce distinct legacy and stable IDs: %q", legacyID)
			}
			store, err := fridayworktree.NewStore(cfg.ProjectsPath(), proj.ID())
			if err != nil {
				t.Fatal(err)
			}
			items, err := store.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].SessionID != "legacy-session" {
				t.Fatalf("raw-ID legacy migration = %#v", items)
			}
		})
	}
}

func TestTUIProjectUsesStableLogicalIdentityAcrossGitWorktrees(t *testing.T) {
	repo := newCommandGitRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runCommandGit(t, repo, "worktree", "add", "-b", "feature", linked, "HEAD")
	t.Cleanup(func() { _ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", linked).Run() })
	cfg := commandWorktreeConfig(t)

	mainProject, err := prepareLogicalProject(context.Background(), repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	linkedProject, err := prepareLogicalProject(context.Background(), linked, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if mainProject.ID() != linkedProject.ID() {
		t.Fatalf("main project %q != linked project %q", mainProject.ID(), linkedProject.ID())
	}
}

func TestTUIProjectPreservesNonGitPathIdentity(t *testing.T) {
	root := t.TempDir()
	cfg := commandWorktreeConfig(t)
	proj, err := prepareLogicalProject(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := projectpkg.CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := proj.ID(), projectpkg.ProjectID(canonical); got != want {
		t.Fatalf("non-Git project ID = %q, want %q", got, want)
	}
}

func commandWorktreeConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.Worktree.Directory = filepath.Join(t.TempDir(), "worktrees")
	return cfg
}

func newCommandGitRepo(t *testing.T) string {
	t.Helper()
	return newCommandGitRepoAt(t, t.TempDir())
}

func newCommandGitRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runCommandGit(t, dir, "init", "-b", "main")
	runCommandGit(t, dir, "config", "user.name", "Friday Tests")
	runCommandGit(t, dir, "config", "user.email", "friday@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommandGit(t, dir, "add", "README.md")
	runCommandGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runCommandGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = commandGitOutput(t, dir, args...)
}

func commandGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func rawLegacyRepositoryID(common string) string {
	clean := filepath.Clean(common)
	base := filepath.Base(clean)
	if base == ".git" {
		base = filepath.Base(filepath.Dir(clean))
	} else {
		base = strings.TrimSuffix(base, ".git")
	}
	if base == "" || base == "." {
		base = "repository"
	}
	sum := sha256.Sum256([]byte(clean))
	return fmt.Sprintf("%s-%x", base, sum[:6])
}

func writeCommandLegacyRegistry(t *testing.T, path, checkout, branch, sessionID string, created time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := struct {
		Version int `json:"version"`
		Entries []struct {
			Path       string    `json:"path"`
			Branch     string    `json:"branch"`
			SessionID  string    `json:"session_id"`
			CreatedAt  time.Time `json:"created_at"`
			LastUsedAt time.Time `json:"last_used_at"`
		} `json:"entries"`
	}{Version: 1}
	doc.Entries = append(doc.Entries, struct {
		Path       string    `json:"path"`
		Branch     string    `json:"branch"`
		SessionID  string    `json:"session_id"`
		CreatedAt  time.Time `json:"created_at"`
		LastUsedAt time.Time `json:"last_used_at"`
	}{Path: checkout, Branch: branch, SessionID: sessionID, CreatedAt: created, LastUsedAt: created})
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
