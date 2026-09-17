package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryHotReloadsSkillFiles(t *testing.T) {
	root := t.TempDir()
	write := func(name, description string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "SKILL.md")
		content := "---\nname: " + name + "\ndescription: " + description + "\n---\nInstructions for " + name
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Second)
		if err := os.Chtimes(path, now, now); err != nil {
			t.Fatal(err)
		}
	}
	write("alpha", "first")
	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(loader)
	if got := registry.List(); len(got) != 1 || got[0].Description != "first" {
		t.Fatalf("initial skills = %#v", got)
	}
	write("alpha", "changed")
	write("beta", "second")
	got := registry.List()
	if len(got) != 2 {
		t.Fatalf("hot-loaded skills = %#v", got)
	}
	alpha, err := registry.Get("alpha")
	if err != nil || alpha.Description != "changed" {
		t.Fatalf("alpha = %#v, err=%v", alpha, err)
	}
}

func TestRegistryHotReloadKeepsLastGoodOnInvalidSkill(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: alpha\ndescription: good\n---\nGood."), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(loader)
	if err := os.WriteFile(path, []byte("---\nname: [broken\n---\nBad."), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	got, err := registry.Get("alpha")
	if err != nil || got.Description != "good" {
		t.Fatalf("last-good skill = %#v, err=%v", got, err)
	}
}
