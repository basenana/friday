package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/coder/project"
)

func overlayTestPath(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	return dir, filepath.Join(dir, "projects", "friday-test", "sandbox.json")
}

func TestLoadProjectAllowMissingFileReturnsNil(t *testing.T) {
	_, path := overlayTestPath(t)
	allow, err := LoadProjectAllow(path)
	if err != nil {
		t.Fatalf("LoadProjectAllow(missing) error = %v", err)
	}
	if allow != nil {
		t.Fatalf("LoadProjectAllow(missing) = %#v, want nil", allow)
	}
}

func TestAppendProjectAllowRoundTrip(t *testing.T) {
	_, path := overlayTestPath(t)

	if err := AppendProjectAllow(path, "gofmt"); err != nil {
		t.Fatalf("AppendProjectAllow: %v", err)
	}
	if err := AppendProjectAllow(path, "gofmt"); err != nil { // idempotent
		t.Fatalf("AppendProjectAllow(dup): %v", err)
	}
	if err := AppendProjectAllow(path, "staticcheck"); err != nil {
		t.Fatalf("AppendProjectAllow(second): %v", err)
	}

	allow, err := LoadProjectAllow(path)
	if err != nil {
		t.Fatalf("LoadProjectAllow: %v", err)
	}
	if len(allow) != 2 || allow[0] != "gofmt" || allow[1] != "staticcheck" {
		t.Fatalf("allow = %#v, want [gofmt staticcheck]", allow)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
	}
}

func TestLoadProjectAllowTrimsAndDeduplicates(t *testing.T) {
	_, path := overlayTestPath(t)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"allow":["  gofmt ","gofmt","","staticcheck"]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	allow, err := LoadProjectAllow(path)
	if err != nil {
		t.Fatalf("LoadProjectAllow: %v", err)
	}
	if len(allow) != 2 || allow[0] != "gofmt" || allow[1] != "staticcheck" {
		t.Fatalf("allow = %#v, want trimmed [gofmt staticcheck]", allow)
	}
}

func TestLoadProjectAllowRejectsBadVersion(t *testing.T) {
	_, path := overlayTestPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"allow":["gofmt"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectAllow(path); err == nil {
		t.Fatal("version 2 should be rejected")
	} else if !strings.Contains(err.Error(), "version") {
		t.Fatalf("error = %v, want version mention", err)
	}
}

func TestLoadProjectAllowRejectsOpenPermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are skipped for root")
	}
	_, path := overlayTestPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"allow":["gofmt"]}`), 0o666); err != nil {
		t.Fatal(err)
	}
	// Explicit chmod: file creation is subject to umask, which silently turns
	// 0o666 into a more restrictive mode and would void the check under test.
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectAllow(path); err == nil {
		t.Fatal("group/world-writable allow file should be rejected")
	} else if !strings.Contains(err.Error(), "permissions too open") {
		t.Fatalf("error = %v, want permissions mention", err)
	}
}

func TestLoadProjectAllowRejectsWorldWritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are skipped for root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"allow":["gofmt"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if _, err := LoadProjectAllow(path); err == nil {
		t.Fatal("world-writable directory should be rejected")
	} else if !strings.Contains(err.Error(), "world-writable") {
		t.Fatalf("error = %v, want world-writable mention", err)
	}
}

func TestLoadProjectAllowRejectsOversizedFile(t *testing.T) {
	_, path := overlayTestPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, maxConfigFileSize+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectAllow(path); err == nil {
		t.Fatal("oversized allow file should be rejected")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want size mention", err)
	}
}

func TestProjectAllowPathStableUnderSymlink(t *testing.T) {
	real := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}

	dataDir := t.TempDir()
	direct, err := ProjectAllowPath(dataDir, real)
	if err != nil {
		t.Fatal(err)
	}
	viaLink, err := ProjectAllowPath(dataDir, link)
	if err != nil {
		t.Fatal(err)
	}
	if direct != viaLink {
		t.Fatalf("path via symlink = %q, want %q", viaLink, direct)
	}
	if filepath.Base(filepath.Dir(direct)) != project.ProjectID(canonicalRoot(t, real)) {
		t.Fatalf("project dir = %q, want id %q", filepath.Base(filepath.Dir(direct)), project.ProjectID(canonicalRoot(t, real)))
	}
}

func TestProjectAllowPathStableAcrossGitWorktrees(t *testing.T) {
	repo := t.TempDir()
	runOverlayGit(t, repo, "init", "-b", "main")
	runOverlayGit(t, repo, "config", "user.name", "Friday Tests")
	runOverlayGit(t, repo, "config", "user.email", "friday@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOverlayGit(t, repo, "add", "README.md")
	runOverlayGit(t, repo, "commit", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	runOverlayGit(t, repo, "worktree", "add", "-b", "feature", linked, "HEAD")
	t.Cleanup(func() { _ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", linked).Run() })

	dataDir := t.TempDir()
	mainPath, err := ProjectAllowPath(dataDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	linkedPath, err := ProjectAllowPath(dataDir, linked)
	if err != nil {
		t.Fatal(err)
	}
	if mainPath != linkedPath {
		t.Fatalf("linked checkout allow path = %q, want logical project path %q", linkedPath, mainPath)
	}
}

func TestProjectAllowPathMigratesLegacyCheckoutGrantBeforeConfigLoad(t *testing.T) {
	repo := t.TempDir()
	runOverlayGit(t, repo, "init", "-b", "main")
	dataDir := t.TempDir()
	legacyID := project.ProjectID(canonicalRoot(t, repo))
	legacyPath := filepath.Join(dataDir, "projects", legacyID, "sandbox.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"version":1,"allow":["gofmt"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stablePath, err := ProjectAllowPath(dataDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	if stablePath == legacyPath {
		t.Fatal("Git project still resolved to checkout-path identity")
	}
	allow, err := LoadProjectAllow(stablePath)
	if err != nil || len(allow) != 1 || allow[0] != "gofmt" {
		t.Fatalf("migrated allow = %#v, %v", allow, err)
	}
}

func runOverlayGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// canonicalRoot resolves the canonical form of root, working around
// symlinked temp directories (for example macOS /var -> /private/var):
// ProjectID is only stable over canonical roots.
func canonicalRoot(t *testing.T, root string) string {
	t.Helper()
	canonical, err := project.CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestValidateGrantableCommand(t *testing.T) {
	cfg := DefaultConfig() // deny list contains sudo/su

	if err := ValidateGrantableCommand(cfg, "gofmt"); err != nil {
		t.Fatalf("gofmt should be grantable: %v", err)
	}
	if err := ValidateGrantableCommand(cfg, ""); err == nil {
		t.Fatal("empty command should be rejected")
	}
	for _, bad := range []string{"gofmt -l .", "rm -rf /", "gi*t", "a;b", "$HOME"} {
		if err := ValidateGrantableCommand(cfg, bad); err == nil {
			t.Fatalf("%q should be rejected as not a plain command name", bad)
		}
	}
	for _, denied := range DefaultDeniedCommands {
		if err := ValidateGrantableCommand(cfg, denied); err == nil {
			t.Fatalf("deny-listed %q must not be grantable", denied)
		}
	}
}
