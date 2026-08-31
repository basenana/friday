package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfig_EmptyPathUsesDefaults(t *testing.T) {
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
}

func TestLoadConfig_JSONLoadsFullConfig(t *testing.T) {
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
	      "timeout": "30m"
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
				ReadOnly:  []string{"/etc", "/usr/share"},
				Deny:      []string{"/secret"},
				Write:     []string{"/tmp", "/workspace"},
				Protected: []string{"~/.ssh", ".env"},
			},
			Network: NetworkConfig{
				Isolation: false,
				Allow:     []string{"example.com", "api.example.com"},
			},
			Defaults: DefaultsConfig{
				Timeout: "30m",
			},
		},
	}
	if !reflect.DeepEqual(cfg, expected) {
		t.Fatalf("LoadConfig() mismatch\n got: %#v\nwant: %#v", cfg, expected)
	}
}

func TestLoadConfig_YAMLLoadsFullConfig(t *testing.T) {
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
    timeout: 30m
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
	if cfg.Sandbox.Defaults.Timeout != "30m" {
		t.Fatalf("expected timeout 30m, got %q", cfg.Sandbox.Defaults.Timeout)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.ReadOnly, []string{"/etc", "/usr/share"}) {
		t.Fatalf("unexpected readonly: %#v", cfg.Sandbox.Filesystem.ReadOnly)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.Deny, []string{"/secret"}) {
		t.Fatalf("unexpected deny: %#v", cfg.Sandbox.Filesystem.Deny)
	}
	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.Write, []string{"/tmp", "/workspace"}) {
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

func TestLoadConfig_PreservesTildePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, []byte(`{"sandbox":{"filesystem":{"write":["~/sandbox-write"]}}}`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if !reflect.DeepEqual(cfg.Sandbox.Filesystem.Write, []string{"~/sandbox-write"}) {
		t.Fatalf("unexpected write paths: %#v", cfg.Sandbox.Filesystem.Write)
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
