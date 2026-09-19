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
	// The stamp check is rate-limited; age it past the interval so the
	// reload fires (tests control the clock instead of sleeping).
	registry.stampCheckedAt = time.Now().Add(-2 * stampCheckInterval)
	got := registry.List()
	if len(got) != 2 {
		t.Fatalf("hot-loaded skills = %#v", got)
	}
	alpha, err := registry.Get("alpha")
	if err != nil || alpha.Description != "changed" {
		t.Fatalf("alpha = %#v, err=%v", alpha, err)
	}
}

// TestRegistryRateLimitsStampChecks pins the rate-limit contract: changes
// within the interval are not picked up (no stat walk per keystroke), and
// the check runs again once the interval has elapsed.
func TestRegistryRateLimitsStampChecks(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "SKILL.md")
		content := "---\nname: " + name + "\ndescription: " + name + " skill\n---\nInstructions for " + name
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("alpha")
	loader := NewLoader(root)
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(loader)
	if got := registry.List(); len(got) != 1 {
		t.Fatalf("initial skills = %#v", got)
	}

	write("beta")
	if got := registry.List(); len(got) != 1 {
		t.Fatalf("stamp check not rate-limited within interval: %#v", got)
	}

	registry.stampCheckedAt = time.Now().Add(-2 * stampCheckInterval)
	if got := registry.List(); len(got) != 2 {
		t.Fatalf("stamp check after interval did not reload: %#v", got)
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
	// Age the stamp check past the rate-limit interval so the reload fires.
	registry.stampCheckedAt = time.Now().Add(-2 * stampCheckInterval)
	got, err := registry.Get("alpha")
	if err != nil || got.Description != "good" {
		t.Fatalf("last-good skill = %#v, err=%v", got, err)
	}
}
