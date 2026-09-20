package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigValidateRejectsInvalidFilesystemPatterns(t *testing.T) {
	fields := []struct {
		name string
		set  func(*FilesystemConfig, []string)
	}{
		{name: "readonly", set: func(c *FilesystemConfig, v []string) { c.ReadOnly = v }},
		{name: "protected", set: func(c *FilesystemConfig, v []string) { c.Protected = v }},
		{name: "deny", set: func(c *FilesystemConfig, v []string) { c.Deny = v }},
		{name: "write", set: func(c *FilesystemConfig, v []string) { c.Write = v }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			cfg := DefaultConfig()
			field.set(&cfg.Sandbox.Filesystem, []string{"[unterminated"})
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() accepted malformed filesystem glob")
			}
		})
	}
}

func TestConfigValidateAllowsMissingSnapshotPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{
		ReadOnly:  []string{"missing-literal", "missing-*.pem"},
		Protected: []string{"~/.missing-credentials"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected missing snapshot paths: %v", err)
	}
}

func TestConfigValidateRejectsEmptyFilesystemPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem.Deny = []string{"  "}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted empty filesystem path")
	}
}

func TestLoadConfig_EmptyPathUsesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("IS_SANDBOX", "")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}
	if !cfg.Sandbox.Network.Isolation {
		t.Fatal("expected default isolation to be true")
	}
	assertFridayRuntimeWriteRoots(t, cfg, home)
}

func TestLoadConfig_JSONLoadsFullConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("IS_SANDBOX", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	data := []byte(`{
	  "permissions": {
	    "allow": ["bash", "go"],
	    "deny": ["sudo"]
	  },
	  "sandbox": {
	    "enabled": false,
	    "filesystem": {
	      "readonly": ["/etc", "/usr/share"],
	      "deny": ["/secret"],
	      "write": ["/tmp", "/workspace"],
	      "protected": ["~/.ssh", ".env"]
	    },
	    "network": {
	      "isolation": false,
	      "allow": ["example.com", "api.example.com"]
	    },
	    "defaults": {
		      "timeout": "15m"
	    }
	  }
	}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	expected := &Config{
		Permissions: PermissionsConfig{
			Allow: []string{"bash", "go"},
			Deny:  []string{"sudo"},
		},
		Sandbox: SandboxConfig{
			Enabled: false,
			Filesystem: FilesystemConfig{
				ReadOnly: []string{"/etc", "/usr/share"},
				Deny:     []string{"/secret"},
				Write: []string{
					"/tmp",
					"/workspace",
					filepath.Join(home, ".friday", "workspace"),
					filepath.Join(home, ".friday", "memory"),
				},
				Protected: []string{"~/.ssh", ".env"},
			},
			Network: NetworkConfig{
				Isolation: false,
				Allow:     []string{"example.com", "api.example.com"},
			},
			Defaults: DefaultsConfig{
				Timeout: "15m",
			},
		},
	}
	if !reflect.DeepEqual(cfg, expected) {
		t.Fatalf("LoadConfig() mismatch\n got: %#v\nwant: %#v", cfg, expected)
	}
}

func TestLoadConfig_YAMLLoadsFullConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("IS_SANDBOX", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.yaml")
	data := []byte(`permissions:
  allow:
    - bash
    - go
  deny:
    - sudo
sandbox:
  enabled: false
  filesystem:
    readonly:
      - /etc
      - /usr/share
    deny:
      - /secret
    write:
      - /tmp
      - /workspace
    protected:
      - ~/.ssh
      - .env
  network:
    isolation: false
    allow:
      - example.com
      - api.example.com
  defaults:
    timeout: 15m
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.Permissions.Allow[0] != "bash" || cfg.Permissions.Allow[1] != "go" {
		t.Fatalf("unexpected permissions.allow: %#v", cfg.Permissions.Allow)
	}
	if cfg.Permissions.Deny[0] != "sudo" {
		t.Fatalf("unexpected permissions.deny: %#v", cfg.Permissions.Deny)
	}
	if cfg.Sandbox.Enabled {
		t.Fatal("expected sandbox.enabled to be false")
	}
	if cfg.Sandbox.Network.Isolation {
		t.Fatal("expected isolation to be false")
	}
	if cfg.Sandbox.Defaults.Timeout != "15m" {
		t.Fatalf("expected timeout 15m, got %q", cfg.Sandbox.Defaults.Timeout)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.ReadOnly, []string{"/etc", "/usr/share"}) {
		t.Fatalf("unexpected readonly: %#v", cfg.Sandbox.Filesystem.ReadOnly)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.Deny, []string{"/secret"}) {
		t.Fatalf("unexpected deny: %#v", cfg.Sandbox.Filesystem.Deny)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.Write, []string{
		"/tmp",
		"/workspace",
		filepath.Join(home, ".friday", "workspace"),
		filepath.Join(home, ".friday", "memory"),
	}) {
		t.Fatalf("unexpected write: %#v", cfg.Sandbox.Filesystem.Write)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.Protected, []string{"~/.ssh", ".env"}) {
		t.Fatalf("unexpected protected: %#v", cfg.Sandbox.Filesystem.Protected)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Network.Allow, []string{"example.com", "api.example.com"}) {
		t.Fatalf("unexpected network.allow: %#v", cfg.Sandbox.Network.Allow)
	}
}

func TestLoadConfig_PartialConfigPreservesDefaults(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	data := []byte(`{"sandbox":{"network":{"isolation":false}}}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.Sandbox.Network.Isolation {
		t.Fatal("expected isolation to be false from JSON config")
	}
	if cfg.Sandbox.Defaults.Timeout != "5m" {
		t.Fatalf("expected default timeout to be preserved, got %q", cfg.Sandbox.Defaults.Timeout)
	}
	if !cfg.Sandbox.Enabled {
		t.Fatal("expected sandbox.enabled default true to be preserved")
	}
	if len(cfg.Permissions.Allow) == 0 {
		t.Fatal("expected default permissions.allow to be preserved")
	}
}

func TestLoadConfig_NotExistUsesDefaults(t *testing.T) {
	t.Setenv("IS_SANDBOX", "")
	path := filepath.Join(t.TempDir(), "missing.yaml")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}
	if !cfg.Sandbox.Enabled {
		t.Fatal("expected default sandbox.enabled to be true")
	}
	if !cfg.Sandbox.Network.Isolation {
		t.Fatal("expected default network isolation to be true")
	}
}

func TestLoadConfig_InvalidJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, []byte(`{"sandbox":`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadConfig_InvalidYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.yaml")
	if err := os.WriteFile(path, []byte("sandbox:\n  network:\n    isolation: [\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoadConfig_InvalidTimeoutReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, []byte(`{"sandbox":{"defaults":{"timeout":"not-a-duration"}}}`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for invalid timeout, got nil")
	}
}

func TestLoadConfig_RejectsTimeoutAboveMaximum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, []byte(`{"sandbox":{"defaults":{"timeout":"15m1s"}}}`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected timeout above 15m to be rejected")
	}
}

func TestLoadConfig_PreservesTildePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("IS_SANDBOX", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, []byte(`{"sandbox":{"filesystem":{"write":["~/sandbox-write"]}}}`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.Write, []string{
		"~/sandbox-write",
		filepath.Join(home, ".friday", "workspace"),
		filepath.Join(home, ".friday", "memory"),
	}) {
		t.Fatalf("unexpected write paths: %#v", cfg.Sandbox.Filesystem.Write)
	}
}

func assertFridayRuntimeWriteRoots(t *testing.T, cfg *Config, home string) {
	t.Helper()
	for _, name := range []string{"workspace", "memory"} {
		want := filepath.Join(home, ".friday", name)
		if !containsFilesystemRoot(cfg.Sandbox.Filesystem.Write, want) {
			t.Fatalf("missing runtime write root %q in %#v", want, cfg.Sandbox.Filesystem.Write)
		}
	}
}

func TestLoadConfig_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	oversized := make([]byte, maxConfigFileSize+1)
	for i := range oversized {
		oversized[i] = ' '
	}
	if err := os.WriteFile(path, oversized, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for oversized config, got nil")
	}
}

func TestLoadConfig_RejectsGroupWritableFileForNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission check is skipped for root")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, []byte(`{}`), 0664); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Chmod(path, 0664); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for overly permissive config file, got nil")
	}
}
