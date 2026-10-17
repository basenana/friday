package setup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"git.hypo.xyz/agents/friday/config"
	"git.hypo.xyz/agents/friday/core/providers"
	"git.hypo.xyz/agents/friday/core/providers/fallback"
	"git.hypo.xyz/agents/friday/sessions"
	"git.hypo.xyz/agents/friday/sessions/file"
	"git.hypo.xyz/agents/friday/workspace"
)

type setupDecisionProvider struct {
	calls atomic.Int32
}

func (p *setupDecisionProvider) Evaluate(context.Context, providers.DecisionRequest) (providers.DecisionResponse, error) {
	p.calls.Add(1)
	return providers.DecisionResponse{}, errors.New("unexpected setup-time evaluation")
}

func newDecisionSetupFixture(t *testing.T) (*sessions.Manager, *config.Config, *fallback.ModelPool) {
	t.Helper()
	base := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = filepath.Join(base, "data")
	cfg.Workspace = filepath.Join(base, "workspace")
	cfg.Memory.Enabled = false
	cfg.Sandbox.Sandbox.Enabled = false
	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if _, err := ws.InitWithParams(nil); err != nil {
		t.Fatal(err)
	}
	store := file.NewFileSessionStore(cfg.SessionsPath())
	mgr := sessions.NewManager(store, filepath.Join(cfg.DataDirPath(), "current"), "")
	client := &recordingProviderClient{}
	pool := fallback.NewModelPool([]fallback.ModelEntry{{Client: client, Name: "recording"}})
	return mgr, cfg, pool
}

func TestNewAgentFailsWhenAutomationEnabledWithoutDecisionModel(t *testing.T) {
	mgr, cfg, pool := newDecisionSetupFixture(t)
	cfg.DecisionModel = nil
	cfg.Sandbox.Automation.Enabled = true

	_, err := NewAgent(mgr, cfg, WithModelPool(pool))
	if err == nil || !strings.Contains(err.Error(), "sandbox automation requires a configured decision model") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewAgentRejectsInvalidDecisionProviderConfiguration(t *testing.T) {
	mgr, cfg, pool := newDecisionSetupFixture(t)
	cfg.DecisionModel = &config.DecisionModelConfig{Provider: "invalid", Model: "judge"}

	_, err := NewAgent(mgr, cfg, WithModelPool(pool))
	if err == nil || !strings.Contains(err.Error(), "unknown decision provider: invalid") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewAgentDoesNotEvaluateInjectedDecisionProviderDuringSetup(t *testing.T) {
	mgr, cfg, pool := newDecisionSetupFixture(t)
	cfg.Sandbox.Automation.Enabled = true
	cfg.DecisionModel = &config.DecisionModelConfig{Provider: "jev", Model: "judge"}
	provider := &setupDecisionProvider{}

	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool), withDecisionProvider(provider))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()
	if calls := provider.calls.Load(); calls != 0 {
		t.Fatalf("Evaluate calls during setup = %d, want 0", calls)
	}
}

func TestNewAgentWithoutDecisionModelPreservesDefaultBehavior(t *testing.T) {
	mgr, cfg, pool := newDecisionSetupFixture(t)
	cfg.DecisionModel = nil
	cfg.Sandbox.Automation.Enabled = false

	agentCtx, err := NewAgent(mgr, cfg, WithModelPool(pool))
	if err != nil {
		t.Fatal(err)
	}
	defer agentCtx.Close()
}
