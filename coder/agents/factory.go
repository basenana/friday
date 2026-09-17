package agents

import (
	"fmt"

	coreagents "github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/core/tools"
)

// ClientFactory builds providers.Client instances per AgentSpec.
// Agent views share the primary client's model pool when it is forkable.
type ClientFactory struct {
	primaryClient providers.Client
	invoker       *tools.Invoker
}

// SetInvoker configures the shared tool invocation pipeline for agents built
// by this factory.
func (f *ClientFactory) SetInvoker(invoker *tools.Invoker) { f.invoker = invoker }

// NewClientFactory returns a factory that forks an isolated lightweight client
// view for every Agent. This keeps Agent runtime observability separate from
// the primary Session client even when the Agent has no policy overrides.
func NewClientFactory(primary providers.Client) *ClientFactory {
	return &ClientFactory{primaryClient: primary}
}

func (f *ClientFactory) ClientFor(model, effort string) (providers.Client, error) {
	forkable, ok := f.primaryClient.(providers.ForkableClient)
	if !ok {
		return nil, fmt.Errorf("provider client does not support isolated agent views")
	}
	return forkable.Fork(providers.ClientPolicy{PreferredModel: model, Effort: effort}), nil
}

// BuildAgent constructs a coreagents.Agent for the given spec, with tools
// filtered by the spec's ToolPolicy.
func (f *ClientFactory) BuildAgent(spec *AgentSpec, allTools []*tools.Tool) (coreagents.Agent, error) {
	if spec == nil {
		return nil, fmt.Errorf("nil agent spec")
	}
	client, err := f.ClientFor(spec.Model, spec.Effort)
	if err != nil {
		return nil, fmt.Errorf("build client for agent %s: %w", spec.Name, err)
	}
	maxLoop := spec.MaxLoopTimes
	if maxLoop == 0 {
		maxLoop = 100
	}
	return coreagents.New(client, coreagents.Option{
		SystemPrompt: spec.SystemPrompt,
		Tools:        spec.ToolPolicy.Apply(allTools),
		MaxLoopTimes: maxLoop,
		Invoker:      f.invoker,
	}), nil
}

// BuildExpertAgents constructs subagents.ExpertAgent entries for the given specs.
// Each expert agent gets its own provider client (per spec.Model) and filtered tools.
func (f *ClientFactory) BuildExpertAgents(specs []*AgentSpec, allTools []*tools.Tool) ([]subagents.ExpertAgent, error) {
	out := make([]subagents.ExpertAgent, 0, len(specs))
	for _, spec := range specs {
		agent, err := f.BuildAgent(spec, allTools)
		if err != nil {
			return nil, err
		}
		out = append(out, subagents.ExpertAgent{
			Name:        spec.Name,
			Description: spec.Description,
			Agent:       agent,
		})
	}
	return out, nil
}

// PrimaryClient returns the factory's primary client.
func (f *ClientFactory) PrimaryClient() providers.Client { return f.primaryClient }
