package agents

import (
	"context"
	"testing"

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

type forkableClient struct {
	fakeClient
	policy providers.ClientPolicy
}

func (f *forkableClient) Fork(policy providers.ClientPolicy) providers.Client {
	return &forkableClient{fakeClient: fakeClient{name: "fork"}, policy: policy}
}

func TestClientFactory_ClientForUnconfiguredForksPrimary(t *testing.T) {
	primary := &forkableClient{fakeClient: fakeClient{name: "primary"}}
	f := NewClientFactory(primary)
	got, err := f.ClientFor("", "")
	if err != nil {
		t.Fatalf("ClientFor error: %v", err)
	}
	forked, ok := got.(*forkableClient)
	if !ok || forked == primary {
		t.Fatalf("client = %T, want isolated fork", got)
	}
	if forked.policy != (providers.ClientPolicy{}) {
		t.Fatalf("fork policy = %+v, want empty", forked.policy)
	}
}

func TestClientFactory_ClientForConfiguredForksPrimary(t *testing.T) {
	primary := &forkableClient{fakeClient: fakeClient{name: "primary"}}
	f := NewClientFactory(primary)
	got, err := f.ClientFor("gpt-4o-mini", "high")
	if err != nil {
		t.Fatalf("ClientFor error: %v", err)
	}
	forked, ok := got.(*forkableClient)
	if !ok {
		t.Fatalf("client = %T, want forked client", got)
	}
	if forked.policy != (providers.ClientPolicy{PreferredModel: "gpt-4o-mini", Effort: "high"}) {
		t.Fatalf("fork policy = %+v", forked.policy)
	}
}

func TestClientFactory_ClientForRequiresForkableClient(t *testing.T) {
	primary := &fakeClient{name: "primary"}
	f := NewClientFactory(primary)
	if _, err := f.ClientFor("", ""); err == nil {
		t.Fatal("agent view on non-forkable client should fail")
	}
}

func TestBuildAgent_AppliesToolPolicy(t *testing.T) {
	primary := &forkableClient{fakeClient: fakeClient{name: "primary"}}
	f := NewClientFactory(primary)

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
	primary := &forkableClient{fakeClient: fakeClient{name: "primary"}}
	f := NewClientFactory(primary)

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
