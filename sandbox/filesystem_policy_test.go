package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileFilesystemPolicyExpandsAndCanonicalizesRules(t *testing.T) {
	root := canonicalTestDir(t)
	home := filepath.Join(root, "home")
	workdir := filepath.Join(root, "work")
	for _, dir := range []string{home, workdir, filepath.Join(workdir, "readonly")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(workdir, "one.pem"),
		filepath.Join(workdir, "readonly", "nested.pem"),
		filepath.Join(home, "secret"),
	} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(workdir, "secret-link")
	if err := os.Symlink(filepath.Join(home, "secret"), alias); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{
		Write:     []string{"readonly", "missing-write"},
		ReadOnly:  []string{"readonly"},
		Protected: []string{"*.pem", "zero-*.key"},
		Deny:      []string{"~/secret", "secret-link"},
	}
	policy, err := compileFilesystemPolicy(cfg, workdir, home)
	if err != nil {
		t.Fatal(err)
	}

	assertPolicyRule(t, policy, workdir, filesystemRuleWrite, filesystemObjectDirectory)
	assertPolicyRule(t, policy, filepath.Join(workdir, "readonly"), filesystemRuleReadOnly, filesystemObjectDirectory)
	assertPolicyRule(t, policy, filepath.Join(workdir, "one.pem"), filesystemRuleProtected, filesystemObjectFile)
	assertNoPolicyRule(t, policy, filepath.Join(workdir, "readonly", "nested.pem")) // *.pem is non-recursive
	assertNoPolicyRule(t, policy, filepath.Join(workdir, "missing-write"))
	assertNoPolicyRule(t, policy, filepath.Join(workdir, "zero-any.key"))

	secret := filepath.Join(home, "secret")
	assertPolicyRule(t, policy, secret, filesystemRuleDeny, filesystemObjectFile)
	if count := countPolicyPath(policy, secret); count != 1 {
		t.Fatalf("canonical target appears %d times, want one final rule: %#v", count, policy.Rules)
	}
}

func TestCompileFilesystemPolicyAppliesFullPriorityAtSamePath(t *testing.T) {
	workdir := canonicalTestDir(t)
	path := filepath.Join(workdir, "shared")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{
		Write: []string{path}, ReadOnly: []string{path}, Protected: []string{path}, Deny: []string{path},
	}
	policy, err := compileFilesystemPolicy(cfg, workdir, workdir)
	if err != nil {
		t.Fatal(err)
	}
	assertPolicyRule(t, policy, path, filesystemRuleDeny, filesystemObjectFile)
	if count := countPolicyPath(policy, path); count != 1 {
		t.Fatalf("priority produced %d rules for one path", count)
	}
}

func TestCompileFilesystemPolicyRejectsHomeRuleWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	workdir := canonicalTestDir(t)
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Protected: []string{"~/.ssh"}}
	if _, err := compileFilesystemPolicy(cfg, workdir, ""); err == nil {
		t.Fatal("expected HOME-relative rule without HOME to fail")
	}
}

func TestCompileFilesystemPolicyRejectsDanglingSymlink(t *testing.T) {
	workdir := canonicalTestDir(t)
	link := filepath.Join(workdir, "dangling")
	if err := os.Symlink(filepath.Join(workdir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Deny: []string{link}}

	if _, err := compileFilesystemPolicy(cfg, workdir, workdir); err == nil {
		t.Fatal("expected dangling symlink to fail closed")
	}
}

func TestCompileFilesystemPolicyRejectsBadGlob(t *testing.T) {
	workdir := canonicalTestDir(t)
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{Protected: []string{"[bad"}}

	if _, err := compileFilesystemPolicy(cfg, workdir, workdir); err == nil {
		t.Fatal("expected malformed glob error")
	}
}

func TestCompileFilesystemPolicyResolvesProtectedGitPathsInLinkedWorktree(t *testing.T) {
	root := canonicalTestDir(t)
	repository := filepath.Join(root, "repository")
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitForPolicyTest(t, repository, "init", "-q")
	runGitForPolicyTest(t, repository, "-c", "user.name=Friday Test", "-c", "user.email=friday@example.com", "commit", "--allow-empty", "-qm", "initial")
	runGitForPolicyTest(t, repository, "worktree", "add", "-q", "-b", "linked", checkout)

	policy, err := compileFilesystemPolicy(DefaultConfig(), checkout, root)
	if err != nil {
		t.Fatalf("compile policy for linked worktree: %v", err)
	}
	assertPolicyRule(t, policy, filepath.Join(repository, ".git", "hooks"), filesystemRuleProtected, filesystemObjectDirectory)
	assertPolicyRule(t, policy, filepath.Join(repository, ".git", "config"), filesystemRuleProtected, filesystemObjectFile)

	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.Write = append(cfg.Sandbox.Filesystem.Write, repository)
	fs := NewLocalFileSystem(NewExecutor(cfg), checkout)
	for _, protected := range []string{
		filepath.Join(repository, ".git", "config"),
		filepath.Join(repository, ".git", "hooks", "new-hook"),
	} {
		if _, err := fs.Resolve(context.Background(), protected, FileAccessWrite); err == nil {
			t.Fatalf("native filesystem write allowed protected linked-worktree Git path %q", protected)
		}
	}

	mainPolicy, err := compileFilesystemPolicy(DefaultConfig(), repository, root)
	if err != nil {
		t.Fatalf("compile policy for main checkout: %v", err)
	}
	assertPolicyRule(t, mainPolicy, filepath.Join(repository, ".git", "hooks"), filesystemRuleProtected, filesystemObjectDirectory)
	assertPolicyRule(t, mainPolicy, filepath.Join(repository, ".git", "config"), filesystemRuleProtected, filesystemObjectFile)
}

func runGitForPolicyTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func TestCloneConfigForProjectResourcesDoesNotMutateSharedPolicy(t *testing.T) {
	cfg := DefaultConfig()
	sharedReadOnly := append([]string(nil), cfg.Sandbox.Filesystem.ReadOnly...)
	sharedWrite := append([]string(nil), cfg.Sandbox.Filesystem.Write...)

	cloned := CloneConfig(cfg)
	cloned.Sandbox.Filesystem.ReadOnly = append(cloned.Sandbox.Filesystem.ReadOnly, "/project/resources")
	cloned.Sandbox.Filesystem.Write = append(cloned.Sandbox.Filesystem.Write, "/project/codebase")

	if len(cfg.Sandbox.Filesystem.ReadOnly) != len(sharedReadOnly) || len(cfg.Sandbox.Filesystem.Write) != len(sharedWrite) {
		t.Fatalf("shared policy mutated: readonly=%#v write=%#v", cfg.Sandbox.Filesystem.ReadOnly, cfg.Sandbox.Filesystem.Write)
	}
	for i := range sharedReadOnly {
		if cfg.Sandbox.Filesystem.ReadOnly[i] != sharedReadOnly[i] {
			t.Fatalf("shared readonly[%d] = %q, want %q", i, cfg.Sandbox.Filesystem.ReadOnly[i], sharedReadOnly[i])
		}
	}
	for i := range sharedWrite {
		if cfg.Sandbox.Filesystem.Write[i] != sharedWrite[i] {
			t.Fatalf("shared write[%d] = %q, want %q", i, cfg.Sandbox.Filesystem.Write[i], sharedWrite[i])
		}
	}
}

func canonicalTestDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertPolicyRule(t *testing.T, policy *filesystemPolicy, path string, kind filesystemRuleKind, object filesystemObjectType) {
	t.Helper()
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range policy.Rules {
		if rule.Path == path {
			if rule.Kind != kind || rule.Object != object {
				t.Fatalf("rule for %q = kind %v object %v, want kind %v object %v", path, rule.Kind, rule.Object, kind, object)
			}
			return
		}
	}
	t.Fatalf("missing rule for %q in %#v", path, policy.Rules)
}

func assertNoPolicyRule(t *testing.T, policy *filesystemPolicy, path string) {
	t.Helper()
	path = filepath.Clean(path)
	for _, rule := range policy.Rules {
		if rule.Path == path {
			t.Fatalf("unexpected rule for %q: %#v", path, rule)
		}
	}
}

func countPolicyPath(policy *filesystemPolicy, path string) int {
	count := 0
	for _, rule := range policy.Rules {
		if rule.Path == path {
			count++
		}
	}
	return count
}
