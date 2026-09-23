//go:build e2e

package setup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers/fallback"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
)

func TestProjectWorktreeAccessBoundary(t *testing.T) {
	runProjectWorktreeAccessBoundaryE2E(t)
}

func runProjectWorktreeAccessBoundaryE2E(t *testing.T) {
	t.Helper()
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	checkout := filepath.Join(base, "external-checkout")
	sibling := filepath.Join(repository, ".worktrees", "sibling")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	runProjectAccessGit(t, repository, "init", "-q")
	runProjectAccessGit(t, repository, "-c", "user.name=Friday Test", "-c", "user.email=friday@example.com", "commit", "--allow-empty", "-qm", "initial")
	runProjectAccessGit(t, repository, "worktree", "add", "-q", "-b", "external-checkout", checkout)
	runProjectAccessGit(t, repository, "worktree", "add", "-q", "-b", "sibling", sibling)
	resources := filepath.Join(base, "data", "projects", "project-id")
	codebase := filepath.Join(resources, "codebase", "INDEX.md")
	metadata := filepath.Join(resources, "project.json")
	denied := filepath.Join(repository, "denied", "secret.txt")
	for _, dir := range []string{filepath.Dir(codebase)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		codebase: "shared index", metadata: "metadata", filepath.Join(sibling, "secret.txt"): "sibling", denied: "denied",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	// The test exercises Friday's capability resolver. Host Seatbelt/bwrap is
	// environment-dependent and is covered by backend policy unit tests.
	cfg.Sandbox.Sandbox.Enabled = false
	cfg.Sandbox.Sandbox.Filesystem.Deny = append(cfg.Sandbox.Sandbox.Filesystem.Deny, filepath.Dir(denied))
	sharedReadOnly := append([]string(nil), cfg.Sandbox.Sandbox.Filesystem.ReadOnly...)
	sharedWrite := append([]string(nil), cfg.Sandbox.Sandbox.Filesystem.Write...)
	store := file.NewFileSessionStore(cfg.SessionsPath())
	manager := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &projectResourceProbeClient{
		paths: map[string]string{
			"codebase": codebase, "metadata": metadata,
			"checkout":      filepath.Join(checkout, "owned.txt"),
			"sibling":       filepath.Join(sibling, "secret.txt"),
			"git_config":    filepath.Join(repository, ".git", "config"),
			"shell_workdir": sibling,
			"denied":        denied,
		},
		results: make(map[string]*tools.Result),
	}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "probe"}})
	agentCtx, err := NewAgent(manager, cfg, WithModelPool(pool), WithTemporary(true), WithWorkdir(checkout), WithProjectResources(resources), WithProjectCodeRoot(repository))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(agentCtx.Close)
	if _, err := api.ReadAllContent(context.Background(), agentCtx.Chat(context.Background(), "probe access")); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"read_codebase", "write_checkout", "read_sibling", "write_sibling", "shell_sibling", "background_sibling"} {
		if result := client.result(name); result == nil || result.IsError {
			t.Fatalf("%s result = %#v, want success", name, result)
		}
	}
	for _, name := range []string{"write_metadata", "write_git_config", "read_denied"} {
		if result := client.result(name); result == nil || !result.IsError {
			t.Fatalf("%s result = %#v, want policy denial", name, result)
		}
	}
	if got, err := os.ReadFile(metadata); err != nil || string(got) != "metadata" {
		t.Fatalf("project metadata changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(checkout, "owned.txt")); err != nil || string(got) != "owned" {
		t.Fatalf("checkout write = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(sibling, "secret.txt")); err != nil || string(got) != "shared" {
		t.Fatalf("sibling write = %q, %v", got, err)
	}
	for _, name := range []string{"shell-owned.txt", "background-owned.txt"} {
		path := filepath.Join(sibling, name)
		deadline := time.Now().Add(2 * time.Second)
		for {
			if _, err := os.Stat(path); err == nil {
				break
			} else if time.Now().After(deadline) {
				t.Fatalf("project-capability command did not create %s: %v", path, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !slices.Equal(cfg.Sandbox.Sandbox.Filesystem.ReadOnly, sharedReadOnly) {
		t.Fatalf("shared sandbox config mutated: %#v", cfg.Sandbox.Sandbox.Filesystem.ReadOnly)
	}
	if !slices.Equal(cfg.Sandbox.Sandbox.Filesystem.Write, sharedWrite) {
		t.Fatalf("shared sandbox write config mutated: %#v", cfg.Sandbox.Sandbox.Filesystem.Write)
	}
}

func runProjectAccessGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
