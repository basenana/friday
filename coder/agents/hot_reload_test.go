package agents

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryHotReloadsAgentSpecs(t *testing.T) {
	root := t.TempDir()
	write := func(name, description string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, SpecFilename)
		content := "---\nname: " + name + "\ndescription: " + description + "\n---\nDo the work."
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Second)
		if err := os.Chtimes(path, now, now); err != nil {
			t.Fatal(err)
		}
	}
	write("writer", "first")
	registry, err := NewLoader(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	write("writer", "changed")
	write("reviewer", "second")
	got := registry.List()
	if len(got) != 2 || got[0].Name != "reviewer" || got[1].Description != "changed" {
		t.Fatalf("hot-loaded agents = %#v", got)
	}
}

func TestRegistryHotReloadKeepsLastGoodWhenValidationFails(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "writer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, SpecFilename)
	if err := os.WriteFile(path, []byte("---\nname: writer\nmodel: good\n---\nWrite."), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewLoader(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetValidator(func(spec *AgentSpec) error {
		if spec.Model != "good" {
			return os.ErrInvalid
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: writer\nmodel: bad\n---\nWrite badly."), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	got := registry.List()
	if len(got) != 1 || got[0].Model != "good" {
		t.Fatalf("last-good agents = %#v", got)
	}
}
