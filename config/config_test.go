package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadForDirPrefersProjectAndKeepsConfigIndependent(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	writeTestConfig(t, filepath.Join(home, ".friday", "config.json"), `{
  "model": {"provider":"openai","model":"home-model","key":"home-secret"},
  "workspace":"global-workspace"
}`)
	writeTestConfig(t, filepath.Join(project, ".friday", "config.json"), `{
  "model": {"provider":"anthropic","model":"project-model"},
  "sandbox": {"sandbox":{"filesystem":{"write":[]}}}
}`)

	cfg, err := LoadForDir("", project)
	if err != nil {
		t.Fatalf("LoadForDir() error = %v", err)
	}
	if !cfg.ProjectScoped() {
		t.Fatal("expected project scope")
	}
	if cfg.Model.Model != "project-model" || cfg.Model.Key != "" {
		t.Fatalf("project config unexpectedly inherited HOME model: %#v", cfg.Model)
	}
	wantWorkspace := filepath.Join(project, ".friday", "workspace")
	if cfg.WorkspacePath() != wantWorkspace {
		t.Fatalf("WorkspacePath() = %q, want %q", cfg.WorkspacePath(), wantWorkspace)
	}
	wantFallback := filepath.Join(home, ".friday", "global-workspace")
	if got := cfg.WorkspaceFallbackPaths(); len(got) != 1 || got[0] != wantFallback {
		t.Fatalf("WorkspaceFallbackPaths() = %v, want [%s]", got, wantFallback)
	}
	if cfg.DataDirPath() != filepath.Join(home, ".friday") {
		t.Fatalf("DataDirPath() = %q, want HOME data", cfg.DataDirPath())
	}
	if cfg.CachesPath() != filepath.Join(home, ".friday", "caches") {
		t.Fatalf("CachesPath() = %q, want HOME caches", cfg.CachesPath())
	}
	for _, name := range []string{"workspace", "memory"} {
		want := filepath.Join(home, ".friday", name)
		if !hasPath(cfg.Sandbox.Sandbox.Filesystem.Write, want) {
			t.Fatalf("missing runtime write root %q in %#v", want, cfg.Sandbox.Sandbox.Filesystem.Write)
		}
	}
}

func TestLoadForDirEmptyProjectDirectoryFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, ".friday"), 0o755); err != nil {
		t.Fatal(err)
	}
	homeConfig := filepath.Join(home, ".friday", "friday.yaml")
	writeTestConfig(t, homeConfig, "model:\n  model: home-yaml\n")

	cfg, err := LoadForDir("", project)
	if err != nil {
		t.Fatalf("LoadForDir() error = %v", err)
	}
	if cfg.ProjectScoped() {
		t.Fatal("empty project .friday should not activate project scope")
	}
	if cfg.ConfigPath() != homeConfig || cfg.Model.Model != "home-yaml" {
		t.Fatalf("unexpected HOME fallback: path=%q model=%q", cfg.ConfigPath(), cfg.Model.Model)
	}
}

func TestLoadForDirPriorityAndRelativePaths(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	explicitDir := t.TempDir()
	t.Setenv("HOME", home)
	writeTestConfig(t, filepath.Join(home, ".friday", "config.json"), `{"model":{"model":"home"}}`)
	writeTestConfig(t, filepath.Join(project, ".friday", "friday.yaml"), "model:\n  model: yaml\n")
	writeTestConfig(t, filepath.Join(project, ".friday", "config.json"), `{"model":{"model":"json"}}`)
	explicit := filepath.Join(explicitDir, "custom.yaml")
	writeTestConfig(t, explicit, "model:\n  model: explicit\nworkspace: context\ndata_dir: data\n")

	cfg, err := LoadForDir("", project)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "json" {
		t.Fatalf("local JSON should win over YAML, got %q", cfg.Model.Model)
	}

	cfg, err = LoadForDir(explicit, project)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "explicit" {
		t.Fatalf("explicit config should win, got %q", cfg.Model.Model)
	}
	if cfg.WorkspacePath() != filepath.Join(explicitDir, "context") {
		t.Fatalf("relative workspace resolved to %q", cfg.WorkspacePath())
	}
	if cfg.DataDirPath() != filepath.Join(explicitDir, "data") {
		t.Fatalf("relative data dir resolved to %q", cfg.DataDirPath())
	}
}

func TestLoadForDirDoesNotSearchParents(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	child := filepath.Join(project, "child")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestConfig(t, filepath.Join(home, ".friday", "config.json"), `{"model":{"model":"home"}}`)
	writeTestConfig(t, filepath.Join(project, ".friday", "config.json"), `{"model":{"model":"parent"}}`)

	cfg, err := LoadForDir("", child)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Model != "home" || cfg.ProjectScoped() {
		t.Fatalf("parent config was unexpectedly discovered: model=%q project=%v", cfg.Model.Model, cfg.ProjectScoped())
	}
}

func TestLoadForDirDoesNotHideBrokenProjectConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeTestConfig(t, filepath.Join(home, ".friday", "config.json"), `{"model":{"model":"home"}}`)
	writeTestConfig(t, filepath.Join(project, ".friday", "config.json"), `{broken`)

	if _, err := LoadForDir("", project); err == nil {
		t.Fatal("broken selected project config should return an error")
	}
}

func TestLoadForDirDisablesSandboxWhenProcessIsSandboxed(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("IS_SANDBOX", "1")
	writeTestConfig(t, filepath.Join(project, ".friday", "config.json"), `{
  "sandbox": {
    "permissions": {"allow": ["echo"], "deny": ["rm"]},
    "sandbox": {
      "enabled": true,
      "filesystem": {
        "readonly": ["/readonly"],
        "deny": ["/denied"],
        "write": ["/write"],
        "protected": ["/protected"]
      },
      "network": {"isolation": true, "allow": ["example.com"]}
    }
  }
}`)

	cfg, err := LoadForDir("", project)
	if err != nil {
		t.Fatalf("LoadForDir() error = %v", err)
	}
	if !cfg.Sandbox.IsolationDisabled() {
		t.Fatal("expected runtime sandbox isolation to be disabled")
	}
	if cfg.Sandbox.Sandbox.Enabled {
		t.Fatal("expected OS sandbox to be disabled")
	}
	if cfg.Sandbox.Sandbox.Network.Isolation {
		t.Fatal("expected network isolation to be disabled")
	}
	if len(cfg.Sandbox.Permissions.Allow) != 1 || cfg.Sandbox.Permissions.Allow[0] != "*" || len(cfg.Sandbox.Permissions.Deny) != 0 {
		t.Fatalf("unexpected permissions after override: %#v", cfg.Sandbox.Permissions)
	}
	filesystem := cfg.Sandbox.Sandbox.Filesystem
	if len(filesystem.ReadOnly) != 0 || len(filesystem.Deny) != 0 || len(filesystem.Write) != 0 || len(filesystem.Protected) != 0 {
		t.Fatalf("unexpected filesystem policy after override: %#v", filesystem)
	}
}

func TestLoadForDirRequiresExactSandboxEnvironmentValue(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes"} {
		t.Run(value, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("IS_SANDBOX", value)

			cfg, err := LoadForDir("", t.TempDir())
			if err != nil {
				t.Fatalf("LoadForDir() error = %v", err)
			}
			if cfg.Sandbox.IsolationDisabled() {
				t.Fatalf("IS_SANDBOX=%q unexpectedly disabled isolation", value)
			}
			if !cfg.Sandbox.Sandbox.Enabled {
				t.Fatalf("IS_SANDBOX=%q unexpectedly disabled OS sandbox", value)
			}
		})
	}
}

func writeTestConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
