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
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/setup"
)

// TestCoder_CommandRegistry verifies the command registry registers and
// dispatches all built-in and agent-backed commands.
func TestCoder_CommandRegistry(t *testing.T) {
	reg := codercmds.NewRegistry()
	codercmds.RegisterBuiltins(reg)
	codercmds.RegisterInfoCommands(reg)
	codercmds.RegisterAgentCommands(reg)

	expected := []string{"clear", "new", "quit", "help", "cost", "context", "compact", "model", "session", "plan", "review", "advisor"}
	for _, name := range expected {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("expected command %q to be registered", name)
		}
	}

	// Alias resolution
	if _, ok := reg.Lookup("exit"); !ok {
		t.Error("alias 'exit' should resolve to 'quit'")
	}
	if _, ok := reg.Lookup("sessions"); !ok {
		t.Error("alias 'sessions' should resolve to 'session'")
	}
}

// TestCoder_Commands_PlanReturnsRunAgent verifies /plan command produces a
// Result that signals agent delegation.
func TestCoder_Commands_PlanReturnsRunAgent(t *testing.T) {
	reg := codercmds.NewRegistry()
	codercmds.RegisterAgentCommands(reg)

	cmd, ok := reg.Lookup("plan")
	if !ok {
		t.Fatal("plan command not found")
	}
	result, err := cmd.Execute(&codercmds.Context{Args: []string{"implement", "login"}})
	if err != nil {
		t.Fatalf("plan Execute error: %v", err)
	}
	if result.RunAgent != coderagents.NamePlanner {
		t.Errorf("plan RunAgent = %q, want %q", result.RunAgent, coderagents.NamePlanner)
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

// TestCoder_AdvisorAgentE2E verifies the advisor agent runs end-to-end via
// run_task delegation and produces structured output.
func TestCoder_AdvisorAgentE2E(t *testing.T) {
	cfg := loadConfig(t)

	withRetry(t, cfg, func(attempt int) error {
		client := newClient(t, cfg, "chat")
		exec := newExecutor(t, cfg)
		workdir := t.TempDir()

		// Create a tiny project for the advisor to examine.
		if err := os.WriteFile(filepath.Join(workdir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			return err
		}

		allTools := newBashFsTools(t, exec, workdir)
		advisorSpec := coderagents.AdvisorSpec(config.ModelConfig{})
		filteredTools := advisorSpec.ToolPolicy.Apply(allTools)

		factory := coderagents.NewClientFactory(client, config.ModelConfig{}, setup.CreateProviderClientFromModel)
		expertAgents, err := factory.BuildExpertAgents([]*coderagents.AgentSpec{advisorSpec}, filteredTools)
		if err != nil {
			return err
		}

		hook := subagents.NewHook(client, subagents.Option{
			ExpertTools:  filteredTools,
			ExpertAgents: expertAgents,
		})

		sess := newTestSession(t, client)
		sess.RegisterHook(hook)
		mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})

		ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
		defer cancel()

		resp := mainAgent.Chat(ctx, &api.Request{
			Session:     sess,
			UserMessage: "Delegate to the 'advisor' subagent: ask it 'Is this project well-structured?'. Report back what the advisor says.",
		})
		content, _ := collectResponse(t, ctx, resp)

		if !historyHasToolCall(sess, "run_task", 1) {
			return errAssertion{msg: "run_task not called"}
		}
		// The advisor should produce some structured guidance.
		if len(content) < 20 {
			return errAssertion{msg: "advisor output too short: " + truncate(content, 200)}
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
		Model:  base,
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

// TestCoder_SessionIsolation verifies that running an agent via run_task does
// not leak the subagent's internal tool calls into the main session history.
func TestCoder_SessionIsolation(t *testing.T) {
	cfg := loadConfig(t)
	client := newClient(t, cfg, "chat")
	exec := newExecutor(t, cfg)
	workdir := t.TempDir()

	allTools := newBashFsTools(t, exec, workdir)
	plannerSpec := coderagents.PlannerSpec(config.ModelConfig{})
	filteredTools := plannerSpec.ToolPolicy.Apply(allTools)

	factory := coderagents.NewClientFactory(client, config.ModelConfig{}, setup.CreateProviderClientFromModel)
	expertAgents, err := factory.BuildExpertAgents([]*coderagents.AgentSpec{plannerSpec}, filteredTools)
	if err != nil {
		t.Fatalf("build expert agents: %v", err)
	}

	hook := subagents.NewHook(client, subagents.Option{
		ExpertTools:  filteredTools,
		ExpertAgents: expertAgents,
	})

	sess := newTestSession(t, client)
	sess.RegisterHook(hook)
	mainAgent := agents.New(client, agents.Option{MaxLoopTimes: 10})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout())
	defer cancel()

	resp := mainAgent.Chat(ctx, &api.Request{
		Session:     sess,
		UserMessage: "Delegate to the 'planner' subagent: ask it to plan 'add a README file'. Relay the planner's response.",
	})
	collectResponse(t, ctx, resp)

	// The main session should have run_task but not fs_read (which is internal
	// to the planner subagent).
	assertHistoryHasToolCall(t, sess, "run_task")
	for _, msg := range sess.GetHistory() {
		for _, tc := range msg.ToolCalls {
			if tc.Name == "fs_read" {
				t.Errorf("main session should not contain fs_read tool call (planner leaked): %+v", tc)
			}
		}
	}
}

// keep import
var _ = types.NewID
