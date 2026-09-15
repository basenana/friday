package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/basenana/friday/workspace"
)

func TestLoadConfigRootsLayersAndInfersTransports(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	project := filepath.Join(base, "project")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "servers.json"), []byte(`{"mcpServers":{"local":{"command":"npx","args":["server"]},"remote":{"url":"https://old.invalid/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "servers.json"), []byte(`{"servers":{"remote":{"type":"sse","url":"https://new.invalid/sse","includeTools":["read"],"excludeTools":["write"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configs, err := LoadConfigRoots([]workspace.ResourceRoot{
		{Path: home, Scope: workspace.ScopeHome},
		{Path: project, Scope: workspace.ScopeProject},
	})
	if err != nil {
		t.Fatal(err)
	}
	if configs["local"].Type != TransportStdio || configs["local"].Project {
		t.Fatalf("local = %#v", configs["local"])
	}
	if configs["remote"].Type != TransportSSE || !configs["remote"].Project || configs["remote"].URL != "https://new.invalid/sse" {
		t.Fatalf("remote = %#v", configs["remote"])
	}
	if !configs["remote"].allowedTool("read") || configs["remote"].allowedTool("write") || configs["remote"].allowedTool("other") {
		t.Fatal("tool filters not applied")
	}
	if err := os.Remove(filepath.Join(project, "servers.json")); err != nil {
		t.Fatal(err)
	}
	configs, err = LoadConfigRoots([]workspace.ResourceRoot{
		{Path: home, Scope: workspace.ScopeHome},
		{Path: project, Scope: workspace.ScopeProject},
	})
	if err != nil {
		t.Fatal(err)
	}
	if configs["remote"].Project || configs["remote"].URL != "https://old.invalid/mcp" {
		t.Fatalf("revealed HOME remote = %#v", configs["remote"])
	}
}

func TestLoadConfigRootsLaterFilesAndMCPServersWin(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "10-first.json"), []byte(`{"servers":{"shared":{"command":"first"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "20-second.json"), []byte(`{
  "servers":{"shared":{"command":"second"},"alias":{"command":"servers-value"}},
  "mcpServers":{"alias":{"command":"mcp-servers-value"}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configs, err := LoadConfigRoots([]workspace.ResourceRoot{{Path: root, Scope: workspace.ScopeHome}})
	if err != nil {
		t.Fatal(err)
	}
	if configs["shared"].Command != "second" {
		t.Fatalf("later file did not win: %#v", configs["shared"])
	}
	if configs["alias"].Command != "mcp-servers-value" {
		t.Fatalf("mcpServers did not win: %#v", configs["alias"])
	}
}

func TestServerConfigDigestExcludesSource(t *testing.T) {
	a := ServerConfig{Name: "x", Command: "tool", Source: "a"}
	b := ServerConfig{Name: "x", Command: "tool", Source: "b"}
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	if err := b.normalize(); err != nil {
		t.Fatal(err)
	}
	if a.Digest != b.Digest {
		t.Fatalf("source changed digest: %s != %s", a.Digest, b.Digest)
	}
}
