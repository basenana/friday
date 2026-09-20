package sandbox

import (
	"os"
	"path/filepath"
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
