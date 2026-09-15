//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coderagents "github.com/basenana/friday/coder/agents"
	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/setup"
)

// TestCoder_CommandRegistry verifies the command registry registers and
// dispatches all built-in and agent-backed commands.
func TestCoder_CommandRegistry(t *testing.T) {
	reg := codercmds.NewRegistry()
	codercmds.RegisterAll(reg)

	expected := []string{"archive", "clear", "compact", "context", "copy", "delete", "diff", "help", "loop", "model", "open", "plan", "quit", "rename", "resume", "show", "status", "stop", "tasks"}
	for _, name := range expected {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("expected command %q to be registered", name)
		}
	}

	// Alias resolution
	if _, ok := reg.Lookup("exit"); !ok {
		t.Error("alias 'exit' should resolve to 'quit'")
	}
	for _, removed := range []string{"new", "session", "sessions", "cost"} {
		if _, ok := reg.Lookup(removed); ok {
			t.Errorf("removed command %q is still registered", removed)
		}
	}
}

// TestCoder_Commands_PlanEntersPersistentMode verifies /plan changes the
// collaboration mode and forwards the task to the current session.
func TestCoder_Commands_PlanEntersPersistentMode(t *testing.T) {
	reg := codercmds.NewRegistry()
	codercmds.RegisterAgentCommands(reg)

	cmd, ok := reg.Lookup("plan")
	if !ok {
		t.Fatal("plan command not found")
	}
	result, err := cmd.Execute(&codercmds.Context{Args: []string{"implement", "login"}, RawArgs: "implement login"})
	if err != nil {
		t.Fatalf("plan Execute error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("unexpected plan result: %+v", result)
	}
	action, ok := result.Actions[0].(codercmds.SetModeAction)
	if !ok || action.Mode != "plan" || action.Prompt != "implement login" {
		t.Errorf("unexpected plan action: %+v", result.Actions[0])
	}
}

// TestCoder_ToolPolicy_ExplorerIsolation verifies that the explorer's deny
// list actually filters out write/bash tools.
func TestCoder_ToolPolicy_ExplorerIsolation(t *testing.T) {
	cfg := loadConfig(t)
	exec := newExecutor(t, cfg)
	workdir := t.TempDir()
	allTools := newBashFsTools(t, exec, workdir)

	spec := coderagents.ExplorerSpec(config.ModelConfig{})
	filtered := spec.ToolPolicy.Apply(allTools)

	for _, tool := range filtered {
		switch tool.Name {
		case "fs_write", "fs_edit", "fs_delete", "fs_mkdir", "bash":
			t.Errorf("explorer policy should not allow %q", tool.Name)
		}
	}
}

// TestCoder_ExplorerReadOnlyE2E verifies the explorer agent can read but not
// modify files when given its deny-listed tool set.
func TestCoder_ExplorerReadOnlyE2E(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		exec := newExecutor(t, cfg)
		workdir := t.TempDir()

		// Plant a file for the explorer to read.
		const payload = "explorer-payload-98765"
		target := filepath.Join(workdir, "target.txt")
		if err := os.WriteFile(target, []byte(payload), 0o644); err != nil {
			return err
		}

		// Build explorer with deny-listed tools (no write/bash).
		explorerSpec := coderagents.ExplorerSpec(config.ModelConfig{})
		explorerTools := explorerSpec.ToolPolicy.Apply(newBashFsTools(t, exec, workdir))

		factory := coderagents.NewClientFactory(client, config.ModelConfig{}, setup.CreateProviderClientFromModel)
		explorerAgent, err := factory.BuildAgent(explorerSpec, explorerTools)
		if err != nil {
			return err
		}

		hook := subagents.NewHook(client, subagents.Option{
			SelfAgent: &subagents.ExpertAgent{
				Name:  coderagents.NameExplorer,
				Agent: explorerAgent,
			},
			ExploreTools: explorerTools,
		})

		sess := newTestSession(t, client)
		sess.RegisterHook(hook)
		mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})

		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := mainAgent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Use the explore tool to read target.txt in the working directory and report its exact contents.",
		})
		content, _ := collectResponse(t, ctx, resp)

		// The file must be untouched.
		got, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if string(got) != payload {
			return errAssertion{msg: "target.txt was modified by explorer"}
		}
		// The report should mention the payload.
		if !strings.Contains(strings.ToLower(content), strings.ToLower(payload)) {
			return errAssertion{msg: "explorer report did not surface payload; got: " + truncate(content, 300)}
		}
		return nil
	})
}

// TestCoder_AgentModelOverride verifies that the AgentModel config overlay
// correctly merges per-agent model settings over the primary model.
func TestCoder_AgentModelOverride(t *testing.T) {
	base := config.ModelConfig{
		Provider: "openai",
		Model:    "gpt-4o",
		Key:      "primary-key",
	}
	cfg := &config.Config{
		Model: base,
		Agents: map[string]config.ModelConfig{
			"explorer": {Model: "gpt-4o-mini"},
		},
	}

	primary := cfg.PrimaryModel()
	if primary.Model != "gpt-4o" {
		t.Fatalf("primary model = %q, want gpt-4o", primary.Model)
	}

	explorer := cfg.AgentModel("explorer")
	if explorer.Model != "gpt-4o-mini" {
		t.Errorf("explorer model = %q, want gpt-4o-mini", explorer.Model)
	}
	// Overlay should preserve the primary key.
	if explorer.Key != "primary-key" {
		t.Errorf("explorer key = %q, want primary-key (overlay should inherit)", explorer.Key)
	}

	// Unconfigured agent falls back to primary.
	unknown := cfg.AgentModel("nonexistent")
	if unknown.Model != "gpt-4o" {
		t.Errorf("unknown agent model = %q, want gpt-4o (fallback)", unknown.Model)
	}
}
