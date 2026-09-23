package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/sessions"
	sessionfile "github.com/basenana/friday/sessions/file"
)

func TestParsePorcelain(t *testing.T) {
	raw := "worktree /repo/main\nHEAD abc123\nbranch refs/heads/main\n\nworktree /repo/task\nHEAD def456\nbranch refs/heads/friday/task\n\nworktree /repo/detached\nHEAD 987654\ndetached\n"
	items, err := parsePorcelain(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[1].Branch != "friday/task" || !items[2].Detached {
		t.Fatalf("items = %#v", items)
	}
}

func TestServiceProjectCodeRootIsMainCheckout(t *testing.T) {
	repo := newGitRepo(t)
	svc, err := Open(context.Background(), repo, filepath.Join(repo, ".worktrees"), "friday/", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.ProjectCodeRoot(); got != want {
		t.Fatalf("ProjectCodeRoot() = %q, want %q", got, want)
	}
}

func TestCreateResolveAndRollback(t *testing.T) {
	repo := newGitRepo(t)
	root := filepath.Join(t.TempDir(), "linked")
	data := filepath.Join(t.TempDir(), "data")
	svc, err := Open(context.Background(), repo, root, "friday/", data)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "add login")
	if err != nil {
		t.Fatal(err)
	}
	if created.Worktree.Branch != "friday/add-login" {
		t.Fatalf("branch = %q", created.Worktree.Branch)
	}
	if _, err := os.Stat(created.Worktree.Path); err != nil {
		t.Fatalf("created path: %v", err)
	}
	if err := svc.SetCurrentPath(created.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	resolved, err := svc.Resolve(context.Background(), "add-login")
	if err != nil || resolved.Path != created.Worktree.Path {
		t.Fatalf("Resolve() = %#v, %v", resolved, err)
	}
	if err := created.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created.Worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists: %v", err)
	}
	if out, _ := exec.Command("git", "-C", repo, "branch", "--list", created.Worktree.Branch).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("branch still exists: %s", out)
	}
	items, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if sameTestPath(item.Path, created.Worktree.Path) {
			t.Fatalf("rolled-back worktree remains registered: %#v", item)
		}
	}
}

func TestCreateUsesSemanticNameGeneratorAndBoundsCollisionSuffix(t *testing.T) {
	repo := newGitRepo(t)
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetNameGenerator(func(context.Context, string) (string, error) {
		return "improve-authentication", nil
	})
	first, err := svc.Create(context.Background(), "改进用户认证")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Rollback(context.Background()) })
	second, err := svc.Create(context.Background(), "改进用户认证")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Rollback(context.Background()) })
	if got := strings.TrimPrefix(first.Worktree.Branch, "friday/"); got != "improve-authenticati" {
		t.Fatalf("first name = %q", got)
	}
	if got := strings.TrimPrefix(second.Worktree.Branch, "friday/"); got != "improve-authentica-2" || len(got) > 20 {
		t.Fatalf("second name = %q, length %d", got, len(got))
	}
}

func TestCreateDoesNotOverwriteExistingDirectory(t *testing.T) {
	repo := newGitRepo(t)
	root := filepath.Join(t.TempDir(), "linked")
	svc, err := Open(context.Background(), repo, root, "", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, svc.RepositoryID(), "task"), 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Rollback(context.Background()) })
	if filepath.Base(created.Worktree.Path) == "task" {
		t.Fatalf("Create reused existing directory %q", created.Worktree.Path)
	}
}

func TestOpenFromSubdirectoryFindsCurrentWorktree(t *testing.T) {
	repo := newGitRepo(t)
	subdir := filepath.Join(repo, "nested", "package")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc, err := Open(context.Background(), subdir, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := svc.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sameTestPath(current.Path, repo) {
		t.Fatalf("current path = %q, want %q", current.Path, repo)
	}
}

func TestMainAndLinkedCheckoutsShareProjectIdentity(t *testing.T) {
	repo := newGitRepo(t)
	data := filepath.Join(t.TempDir(), "data")
	main, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", data)
	if err != nil {
		t.Fatal(err)
	}
	created, err := main.Create(context.Background(), "identity")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = created.Rollback(context.Background()) })

	linked, err := Open(context.Background(), created.Worktree.Path, filepath.Join(t.TempDir(), "linked"), "friday/", data)
	if err != nil {
		t.Fatal(err)
	}
	mainIdentity, linkedIdentity := main.ProjectIdentity(), linked.ProjectIdentity()
	if mainIdentity != linkedIdentity {
		t.Fatalf("main identity = %+v, linked identity = %+v", mainIdentity, linkedIdentity)
	}
	common := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(common) {
		common = filepath.Join(repo, common)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mainIdentity.Repository, filepath.Clean(common); got != want {
		t.Fatalf("repository = %q, want canonical Git common directory %q", got, want)
	}

	store := project.NewFileStore(filepath.Join(data, "projects"))
	mainProject, err := project.OpenWithIdentity(repo, mainIdentity, store)
	if err != nil {
		t.Fatal(err)
	}
	linkedProject, err := project.OpenWithIdentity(created.Worktree.Path, linkedIdentity, store)
	if err != nil {
		t.Fatal(err)
	}
	if mainProject.ID() != linkedProject.ID() {
		t.Fatalf("project IDs = %q and %q", mainProject.ID(), linkedProject.ID())
	}
}

func TestSameNamedRepositoriesHaveDistinctProjectIdentity(t *testing.T) {
	repoA := newGitRepoAt(t, filepath.Join(t.TempDir(), "same-name"))
	repoB := newGitRepoAt(t, filepath.Join(t.TempDir(), "same-name"))
	serviceA, err := Open(context.Background(), repoA, filepath.Join(t.TempDir(), "linked"), "", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	serviceB, err := Open(context.Background(), repoB, filepath.Join(t.TempDir(), "linked"), "", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if serviceA.ProjectIdentity().ID == serviceB.ProjectIdentity().ID {
		t.Fatalf("same-name repositories share identity %q", serviceA.ProjectIdentity().ID)
	}
}

func TestOpenAcceptsRepositoryNameWithBackslash(t *testing.T) {
	repo := newGitRepoAt(t, filepath.Join(t.TempDir(), " repo\\name "))
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatalf("Open() failed for a valid repository name: %v", err)
	}
	if got := svc.ProjectIdentity().ID; !validWorktreeID(got) {
		t.Fatalf("project identity ID = %q, want a safe path component", got)
	}
}

func TestRegistryAssociatesAndReportsStaleWorktree(t *testing.T) {
	repo := newGitRepo(t)
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "registry task")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Associate(created.Worktree.Path, created.Worktree.Branch, "project-1", "session-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Associate(created.Worktree.Path, "friday/renamed", "project-1", "session-1"); err != nil {
		t.Fatal(err)
	}
	items, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	associated, err := svc.Resolve(context.Background(), created.Worktree.Branch)
	if err != nil || associated.ProjectID != svc.ProjectIdentity().ID || associated.SessionID != "session-1" {
		t.Fatalf("association = %#v, %v; items=%#v", associated, err, items)
	}
	runGit(t, repo, "worktree", "remove", created.Worktree.Path)
	items, err = svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundStale := false
	for _, item := range items {
		if sameTestPath(item.Path, created.Worktree.Path) && item.Stale && item.Branch == "friday/renamed" {
			foundStale = true
		}
	}
	if !foundStale {
		t.Fatalf("stale worktree missing from %#v", items)
	}
	runGit(t, repo, "branch", "-d", created.Worktree.Branch)
}

func TestListMarksGitPrunableWorktreeStale(t *testing.T) {
	repo := newGitRepo(t)
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "prunable checkout")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(created.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	porcelain := runGitOutput(t, repo, "worktree", "list", "--porcelain")
	if !strings.Contains(porcelain, "\nprunable ") {
		t.Fatalf("Git did not report the missing checkout as prunable:\n%s", porcelain)
	}

	items, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if sameTestPath(item.Path, created.Worktree.Path) {
			if !item.Stale {
				t.Fatalf("prunable worktree classified as live: %#v", item)
			}
			return
		}
	}
	t.Fatalf("prunable worktree missing from %#v", items)
}

func TestServiceRemoveRetainsBranchAndArchivesReferencedSession(t *testing.T) {
	repo := newGitRepo(t)
	data := filepath.Join(t.TempDir(), "data")
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", data)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "remove normally")
	if err != nil {
		t.Fatal(err)
	}
	manager := sessions.NewManager(sessionfile.NewFileSessionStore(filepath.Join(data, "sessions")), filepath.Join(data, "current"), "")
	lifecycle, err := manager.CreateRoot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Associate(created.Worktree.Path, created.Worktree.Branch, svc.RepositoryID(), sessionID); err != nil {
		t.Fatal(err)
	}
	meta, err := svc.store.Get(created.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Remove(context.Background(), meta.ID, RemoveOptions{Sessions: manager}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created.Worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("removed checkout still exists: %v", err)
	}
	if _, err := svc.store.Get(meta.ID); err == nil {
		t.Fatal("removed worktree metadata still exists")
	}
	if active, err := manager.IsActive(sessionID); err != nil || active {
		t.Fatalf("referenced session active = %t, err = %v; want archived", active, err)
	}
	if exists, err := manager.Exists(sessionID); err != nil || !exists {
		t.Fatalf("referenced session exists = %t, err = %v; want retained", exists, err)
	}
	if branch := strings.TrimSpace(runGitOutput(t, repo, "branch", "--list", created.Worktree.Branch)); branch == "" {
		t.Fatal("default removal deleted the branch")
	}
}

func TestServiceRemoveStaleMetadataSkipsGitAndMissingSession(t *testing.T) {
	repo := newGitRepo(t)
	data := filepath.Join(t.TempDir(), "data")
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", data)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "stale removal")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Associate(created.Worktree.Path, created.Worktree.Branch, svc.RepositoryID(), "missing-session"); err != nil {
		t.Fatal(err)
	}
	meta, err := svc.store.Get(created.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "worktree", "remove", created.Worktree.Path)
	manager := sessions.NewManager(sessionfile.NewFileSessionStore(filepath.Join(data, "sessions")), filepath.Join(data, "current"), "")

	if err := svc.Remove(context.Background(), meta.ID, RemoveOptions{Sessions: manager}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.Get(meta.ID); err == nil {
		t.Fatal("stale metadata still exists")
	}
	if branch := strings.TrimSpace(runGitOutput(t, repo, "branch", "--list", created.Worktree.Branch)); branch == "" {
		t.Fatal("stale cleanup deleted the branch without --delete-branch")
	}
}

func TestServiceRemoveDeleteBranchIsSafeAndRetryable(t *testing.T) {
	repo := newGitRepo(t)
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "unmerged branch")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.Worktree.Path, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, created.Worktree.Path, "add", "feature.txt")
	runGit(t, created.Worktree.Path, "commit", "-m", "feature")
	meta, err := svc.store.Get(created.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}

	err = svc.Remove(context.Background(), meta.ID, RemoveOptions{DeleteBranch: true})
	if err == nil || !strings.Contains(err.Error(), "not fully merged") || !strings.Contains(err.Error(), "retry") || strings.Contains(err.Error(), "the unmerged branch") {
		t.Fatalf("first Remove error = %v; want actual Git detail and retry guidance without assuming the cause", err)
	}
	if _, err := os.Stat(created.Worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("failed branch deletion restored checkout: %v", err)
	}
	if _, err := svc.store.Get(meta.ID); err != nil {
		t.Fatalf("failed branch deletion lost retry metadata: %v", err)
	}
	runGit(t, repo, "merge", "--ff-only", created.Worktree.Branch)

	if err := svc.Remove(context.Background(), meta.ID, RemoveOptions{DeleteBranch: true}); err != nil {
		t.Fatalf("retry Remove: %v", err)
	}
	if branch := strings.TrimSpace(runGitOutput(t, repo, "branch", "--list", created.Worktree.Branch)); branch != "" {
		t.Fatalf("safely deleted branch still exists: %q", branch)
	}
	if _, err := svc.store.Get(meta.ID); err == nil {
		t.Fatal("retry left worktree metadata")
	}
}

func TestServiceRemoveDeleteBranchUsesAndPersistsLiveBranchName(t *testing.T) {
	repo := newGitRepo(t)
	svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.Create(context.Background(), "renamed branch")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := svc.store.Get(created.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}
	renamed := "friday/live-renamed-branch"
	runGit(t, created.Worktree.Path, "branch", "-m", renamed)

	if err := svc.Remove(context.Background(), meta.ID, RemoveOptions{DeleteBranch: true}); err != nil {
		t.Fatal(err)
	}
	if branch := strings.TrimSpace(runGitOutput(t, repo, "branch", "--list", renamed)); branch != "" {
		t.Fatalf("live renamed branch still exists: %q", branch)
	}
	if branch := strings.TrimSpace(runGitOutput(t, repo, "branch", "--list", created.Worktree.Branch)); branch != "" {
		t.Fatalf("stale metadata branch unexpectedly survived as a real branch: %q", branch)
	}
}

func TestServiceRemoveClearsOnlyTargetedPrunableGitRecord(t *testing.T) {
	t.Run("default retains branch and removes ghost row", func(t *testing.T) {
		repo := newGitRepo(t)
		svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
		if err != nil {
			t.Fatal(err)
		}
		created, err := svc.Create(context.Background(), "prunable default")
		if err != nil {
			t.Fatal(err)
		}
		unrelated, err := svc.Create(context.Background(), "unrelated prunable")
		if err != nil {
			t.Fatal(err)
		}
		meta, err := svc.store.Get(created.Worktree.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(created.Worktree.Path); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(unrelated.Worktree.Path); err != nil {
			t.Fatal(err)
		}
		if porcelain := runGitOutput(t, repo, "worktree", "list", "--porcelain"); !strings.Contains(porcelain, created.Worktree.Path) || !strings.Contains(porcelain, "prunable ") {
			t.Fatalf("test did not create a Git-prunable record:\n%s", porcelain)
		}

		if err := svc.Remove(context.Background(), meta.ID, RemoveOptions{}); err != nil {
			t.Fatal(err)
		}
		assertWorktreePathAbsent(t, svc, created.Worktree.Path)
		porcelain := runGitOutput(t, repo, "worktree", "list", "--porcelain")
		if !strings.Contains(porcelain, unrelated.Worktree.Path) || !strings.Contains(porcelain, "prunable ") {
			t.Fatalf("targeted removal pruned an unrelated recoverable record:\n%s", porcelain)
		}
		if branch := strings.TrimSpace(runGitOutput(t, repo, "branch", "--list", created.Worktree.Branch)); branch == "" {
			t.Fatal("default prunable removal deleted the branch")
		}
	})

	t.Run("safe branch deletion remains retryable", func(t *testing.T) {
		repo := newGitRepo(t)
		svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
		if err != nil {
			t.Fatal(err)
		}
		created, err := svc.Create(context.Background(), "prunable retry")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(created.Worktree.Path, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, created.Worktree.Path, "add", "feature.txt")
		runGit(t, created.Worktree.Path, "commit", "-m", "feature")
		meta, err := svc.store.Get(created.Worktree.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(created.Worktree.Path); err != nil {
			t.Fatal(err)
		}

		err = svc.Remove(context.Background(), meta.ID, RemoveOptions{DeleteBranch: true})
		if err == nil || !strings.Contains(err.Error(), "not fully merged") || !strings.Contains(err.Error(), "retry") {
			t.Fatalf("first Remove error = %v; want actual Git detail and retry guidance", err)
		}
		items, err := svc.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		matches := 0
		for _, item := range items {
			if sameTestPath(item.Path, created.Worktree.Path) {
				matches++
				if !item.Stale || item.ID != meta.ID {
					t.Fatalf("retry row = %#v; want restored stale metadata", item)
				}
			}
		}
		if matches != 1 {
			t.Fatalf("retry list contains %d target rows, want exactly restored metadata: %#v", matches, items)
		}
		porcelain := runGitOutput(t, repo, "worktree", "list", "--porcelain")
		if strings.Contains(porcelain, created.Worktree.Path) {
			t.Fatalf("targeted prunable Git record survived removal:\n%s", porcelain)
		}

		runGit(t, repo, "merge", "--ff-only", created.Worktree.Branch)
		if err := svc.Remove(context.Background(), meta.ID, RemoveOptions{DeleteBranch: true}); err != nil {
			t.Fatalf("retry Remove: %v", err)
		}
		assertWorktreePathAbsent(t, svc, created.Worktree.Path)
		if branch := strings.TrimSpace(runGitOutput(t, repo, "branch", "--list", created.Worktree.Branch)); branch != "" {
			t.Fatalf("safely deleted branch still exists: %q", branch)
		}
	})
}

func assertWorktreePathAbsent(t *testing.T, svc *Service, path string) {
	t.Helper()
	items, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if sameTestPath(item.Path, path) {
			t.Fatalf("removed worktree remains listed: %#v", item)
		}
	}
}

func TestServiceRemoveRefusesDirtyMainCurrentAmbiguousAndRunningTargets(t *testing.T) {
	t.Run("dirty", func(t *testing.T) {
		repo := newGitRepo(t)
		svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
		if err != nil {
			t.Fatal(err)
		}
		created, err := svc.Create(context.Background(), "dirty target")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(created.Worktree.Path, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		meta, _ := svc.store.Get(created.Worktree.Path)
		err = svc.Remove(context.Background(), meta.ID, RemoveOptions{})
		if err == nil || !strings.Contains(err.Error(), "commit or stash") {
			t.Fatalf("Remove error = %v; want dirty-checkout guidance", err)
		}
		if _, err := os.Stat(created.Worktree.Path); err != nil {
			t.Fatalf("dirty checkout was removed: %v", err)
		}
		if _, err := svc.store.Get(meta.ID); err != nil {
			t.Fatalf("dirty checkout metadata was removed: %v", err)
		}
		_ = os.Remove(filepath.Join(created.Worktree.Path, "dirty.txt"))
		t.Cleanup(func() { _ = created.Rollback(context.Background()) })
	})

	t.Run("main and current", func(t *testing.T) {
		repo := newGitRepo(t)
		svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Associate(repo, "main", svc.RepositoryID(), ""); err != nil {
			t.Fatal(err)
		}
		entries, err := svc.store.List()
		if err != nil {
			t.Fatal(err)
		}
		var meta Metadata
		for _, entry := range entries {
			if sameTestPath(entry.Path, repo) {
				meta = entry
				break
			}
		}
		if meta.ID == "" {
			t.Fatal("main checkout metadata missing")
		}
		err = svc.Remove(context.Background(), meta.ID, RemoveOptions{})
		if err == nil || !strings.Contains(err.Error(), "main checkout") {
			t.Fatalf("Remove main error = %v", err)
		}

		created, err := svc.Create(context.Background(), "current target")
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.SetCurrentPath(created.Worktree.Path); err != nil {
			t.Fatal(err)
		}
		linked, _ := svc.store.Get(created.Worktree.Path)
		err = svc.Remove(context.Background(), linked.ID, RemoveOptions{})
		if err == nil || !strings.Contains(err.Error(), "current checkout") {
			t.Fatalf("Remove current error = %v", err)
		}
		_ = svc.SetCurrentPath(repo)
		t.Cleanup(func() { _ = created.Rollback(context.Background()) })
	})

	t.Run("ambiguous", func(t *testing.T) {
		repo := newGitRepo(t)
		svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", filepath.Join(t.TempDir(), "data"))
		if err != nil {
			t.Fatal(err)
		}
		first, err := svc.Create(context.Background(), "shared one")
		if err != nil {
			t.Fatal(err)
		}
		second, err := svc.Create(context.Background(), "shared two")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = first.Rollback(context.Background()); _ = second.Rollback(context.Background()) })
		if err := svc.Remove(context.Background(), "friday/shared", RemoveOptions{}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("Remove ambiguous error = %v", err)
		}
	})

	t.Run("running project TUI", func(t *testing.T) {
		repo := newGitRepo(t)
		data := filepath.Join(t.TempDir(), "data")
		svc, err := Open(context.Background(), repo, filepath.Join(t.TempDir(), "linked"), "friday/", data)
		if err != nil {
			t.Fatal(err)
		}
		created, err := svc.Create(context.Background(), "owned target")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = created.Rollback(context.Background()) })
		meta, _ := svc.store.Get(created.Worktree.Path)
		err = svc.Remove(context.Background(), meta.ID, RemoveOptions{AcquireProjectLock: func() (func(), error) {
			return nil, os.ErrExist
		}})
		if err == nil || !strings.Contains(err.Error(), "running project TUI") {
			t.Fatalf("Remove running target error = %v", err)
		}
		if _, err := os.Stat(created.Worktree.Path); err != nil {
			t.Fatalf("running project checkout was removed: %v", err)
		}
	})
}

func sameTestPath(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	return newGitRepoAt(t, t.TempDir())
}

func newGitRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Friday Tests")
	runGit(t, dir, "config", "user.email", "friday@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}
