package agents

import (
	"context"
	"testing"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/tools"
)

// fakeClient is a minimal providers.Client for factory tests.
type fakeClient struct{ name string }

func (f *fakeClient) Completion(_ context.Context, _ providers.Request) providers.Response {
	return providers.NewCommonResponse()
}
func (f *fakeClient) CompletionNonStreaming(_ context.Context, _ providers.Request) (string, error) {
	return "", nil
}
func (f *fakeClient) StructuredPredict(_ context.Context, _ providers.Request, _ any) error {
	return nil
}
func (f *fakeClient) ContextWindow() int64 { return 4096 }

func TestClientFactory_ClientForUnconfiguredReturnsPrimary(t *testing.T) {
	primary := &fakeClient{name: "primary"}
	f := NewClientFactory(primary, config.ModelConfig{}, nil)
	got, err := f.ClientFor(config.ModelConfig{})
	if err != nil {
		t.Fatalf("ClientFor error: %v", err)
	}
	if got != primary {
		t.Fatal("expected primary client for unconfigured model")
	}
}

func TestClientFactory_ClientForConfiguredUsesBuilder(t *testing.T) {
	primary := &fakeClient{name: "primary"}
	secondary := &fakeClient{name: "secondary"}
	calls := 0
	builder := func(mc config.ModelConfig) (providers.Client, error) {
		calls++
		return secondary, nil
	}
	f := NewClientFactory(primary, config.ModelConfig{}, builder)
	cfg := config.ModelConfig{Provider: "openai", Model: "gpt-4o-mini"}
	got, err := f.ClientFor(cfg)
	if err != nil {
		t.Fatalf("ClientFor error: %v", err)
	}
	if got != secondary {
		t.Fatal("expected builder-provided client for configured model")
	}
	if calls != 1 {
		t.Errorf("builder called %d times, want 1", calls)
	}
}

func TestClientFactory_ClientForConfiguredButNilBuilderReturnsPrimary(t *testing.T) {
	primary := &fakeClient{name: "primary"}
	f := NewClientFactory(primary, config.ModelConfig{}, nil)
	cfg := config.ModelConfig{Provider: "openai", Model: "gpt-4o-mini"}
	got, err := f.ClientFor(cfg)
	if err != nil {
		t.Fatalf("ClientFor error: %v", err)
	}
	if got != primary {
		t.Fatal("nil builder should fall back to primary")
	}
}

func TestBuildAgent_AppliesToolPolicy(t *testing.T) {
	primary := &fakeClient{name: "primary"}
	f := NewClientFactory(primary, config.ModelConfig{}, nil)

	all := []*tools.Tool{
		tools.NewTool("fs_read"),
		tools.NewTool("fs_write"),
		tools.NewTool("bash"),
	}
	spec := &AgentSpec{
		Name:         "test",
		SystemPrompt: "test prompt",
		ToolPolicy:   ToolPolicy{Allow: []string{"fs_read"}},
		MaxLoopTimes: 5,
	}
	agent, err := f.BuildAgent(spec, all)
	if err != nil {
		t.Fatalf("BuildAgent error: %v", err)
	}
	if agent == nil {
		t.Fatal("BuildAgent returned nil agent")
	}
}

func TestBuildExpertAgents_CreatesOnePerSpec(t *testing.T) {
	primary := &fakeClient{name: "primary"}
	f := NewClientFactory(primary, config.ModelConfig{}, nil)

	all := []*tools.Tool{tools.NewTool("fs_read"), tools.NewTool("bash")}
	specs := []*AgentSpec{
		{Name: "a", SystemPrompt: "a", ToolPolicy: ToolPolicy{Allow: []string{"fs_read"}}},
		{Name: "b", SystemPrompt: "b", ToolPolicy: ToolPolicy{Allow: []string{"bash"}}},
	}
	experts, err := f.BuildExpertAgents(specs, all)
	if err != nil {
		t.Fatalf("BuildExpertAgents error: %v", err)
	}
	if len(experts) != 2 {
		t.Fatalf("got %d experts, want 2", len(experts))
	}
	if experts[0].Name != "a" || experts[1].Name != "b" {
		t.Errorf("expert names = %q, %q; want %q, %q", experts[0].Name, experts[1].Name, "a", "b")
	}
}
