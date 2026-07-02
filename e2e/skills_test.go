//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/skills"
)

// skillsDir returns the absolute path of e2e/testdata (which contains the
// test-skill directory).
func skillsDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "testdata")
}

// newSkillsRegistry builds a Registry loaded from e2e/testdata.
func newSkillsRegistry(t *testing.T) (*skills.Registry, func()) {
	t.Helper()
	dir := skillsDir(t)
	loader := skills.NewLoader(dir)
	if err := loader.Load(); err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	reg := skills.NewRegistry(loader)
	return reg, func() {}
}

// TestSkills_LoadFromDir verifies the loader picks up the test-skill.
func TestSkills_LoadFromDir(t *testing.T) {
	reg, cleanup := newSkillsRegistry(t)
	defer cleanup()

	s, err := reg.Get("test-skill")
	if err != nil {
		t.Fatalf("Get test-skill: %v", err)
	}
	if s.Instructions == "" {
		t.Error("expected non-empty Instructions")
	}
}

// TestSkills_HookInjectsTools verifies the skills hook injects its toolset
// into an agent request.
func TestSkills_HookInjectsTools(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		reg, cleanup := newSkillsRegistry(t)
		defer cleanup()
		skillsHook := skills.NewHook(reg)

		client := newClient(t, cfg, "chat")
		sess := newTestSession(t, client)
		sess.RegisterHook(skillsHook)

		tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
		agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 10})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Use the list_skills tool to tell me what skills are available. Just list names.",
		})
		collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "list_skills", 1) {
			return errAssertion{msg: "list_skills not invoked"}
		}
		return nil
	})
}

// TestSkills_AgentCallsLoadSkill verifies the LLM invokes load_skill.
func TestSkills_AgentCallsLoadSkill(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		reg, cleanup := newSkillsRegistry(t)
		defer cleanup()
		skillsHook := skills.NewHook(reg)

		client := newClient(t, cfg, "chat")
		sess := newTestSession(t, client)
		sess.RegisterHook(skillsHook)

		tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
		agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 10})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "First use list_skills to see skills, then use load_skill to load the 'test-skill' instructions. Report what you found.",
		})
		collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "load_skill", 1) {
			return errAssertion{msg: "load_skill not invoked"}
		}
		return nil
	})
}

// TestSkills_LoadResource verifies the LLM can load a skill resource file.
func TestSkills_LoadResource(t *testing.T) {
	reg, cleanup := newSkillsRegistry(t)
	defer cleanup()

	data, err := reg.LoadResource("test-skill", "references/ref.txt")
	if err != nil {
		t.Fatalf("LoadResource: %v", err)
	}
	if !strings.Contains(string(data), "ref") {
		t.Errorf("expected ref.txt content, got %q", truncate(string(data), 200))
	}
}

// TestSkills_PathTraversalBlocked verifies that path traversal in resource
// loading is rejected.
func TestSkills_PathTraversalBlocked(t *testing.T) {
	reg, cleanup := newSkillsRegistry(t)
	defer cleanup()

	_, err := reg.LoadResource("test-skill", "../../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal resource load")
	}
}

// TestSkills_RegistryRefresh verifies that Refresh picks up newly-added skills.
func TestSkills_RegistryRefresh(t *testing.T) {
	dir := t.TempDir()
	// Initially empty loader.
	loader := skills.NewLoader(dir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	reg := skills.NewRegistry(loader)

	// Add a new skill directory.
	newSkill := filepath.Join(dir, "fresh-skill")
	if err := os.MkdirAll(newSkill, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newSkill, "SKILL.md"), []byte(`---
name: fresh-skill
description: A dynamically added skill
---

# Fresh Skill
Body.`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := reg.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := reg.Get("fresh-skill"); err != nil {
		t.Errorf("expected fresh-skill to be available after refresh: %v", err)
	}
}

// TestSkills_Delete verifies skill deletion works.
func TestSkills_Delete(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(skillsDir(t), "test-skill")
	dst := filepath.Join(dir, "test-skill")
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader(dir)
	if err := loader.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	reg := skills.NewRegistry(loader)

	if err := reg.Delete("test-skill"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := reg.Get("test-skill"); err == nil {
		t.Error("expected test-skill to be gone after Delete")
	}
}

// TestSkills_InstructionsInPrompt verifies the end-to-end progressive
// disclosure flow: list_skills → load_skill loads SKILL.md instructions into
// the model context, and the loaded skill's behavioural directive takes
// effect. The test-skill instructs the agent to finish responses with
// TEST_SKILL_LOADED, so we assert that phrase appears in the final reply.
func TestSkills_InstructionsInPrompt(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		reg, cleanup := newSkillsRegistry(t)
		defer cleanup()
		skillsHook := skills.NewHook(reg)

		client := newClient(t, cfg, "chat")
		sess := newTestSession(t, client)
		sess.RegisterHook(skillsHook)

		tools := newBashFsTools(t, newExecutor(t, cfg), t.TempDir())
		agent := newReactAgent(t, client, agentOpts{Tools: tools, MaxLoops: 10})
		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := agent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "First use list_skills to see skills, then use load_skill to load the 'test-skill' instructions. After loading, say hello.",
		})
		content, _ := collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "load_skill", 1) {
			return errAssertion{msg: "load_skill not invoked"}
		}
		if !strings.Contains(content, "TEST_SKILL_LOADED") {
			return errAssertion{msg: "response did not contain TEST_SKILL_LOADED; skill instructions may not have been injected: " + truncate(content, 200)}
		}
		return nil
	})
}

// copyDir copies a directory tree recursively.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
