package agents

import (
	"fmt"

	"github.com/basenana/friday/config"
	coreagents "github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/core/tools"
)

// ClientBuilder constructs a providers.Client from a ModelConfig.
// Implementations typically wrap setup.CreateProviderClientFromModel.
type ClientBuilder func(modelCfg config.ModelConfig) (providers.Client, error)

// ClientFactory builds providers.Client instances per AgentSpec.
// When a spec's Model is not configured, the primary client is reused.
type ClientFactory struct {
	primaryClient providers.Client
	primaryCfg    config.ModelConfig
	builder       ClientBuilder
}

// NewClientFactory returns a factory that reuses primary for unconfigured
// agents and falls back to builder for configured ones.
func NewClientFactory(primary providers.Client, primaryCfg config.ModelConfig, builder ClientBuilder) *ClientFactory {
	return &ClientFactory{primaryClient: primary, primaryCfg: primaryCfg, builder: builder}
}

// ClientFor returns the primary client when modelCfg is not configured, or
// builds a new client via the builder otherwise.
func (f *ClientFactory) ClientFor(modelCfg config.ModelConfig) (providers.Client, error) {
	if !modelCfg.IsConfigured() {
		return f.primaryClient, nil
	}
	if f.builder == nil {
		return f.primaryClient, nil
	}
	return f.builder(modelCfg)
}

// BuildAgent constructs a coreagents.Agent for the given spec, with tools
// filtered by the spec's ToolPolicy.
func (f *ClientFactory) BuildAgent(spec *AgentSpec, allTools []*tools.Tool) (coreagents.Agent, error) {
	if spec == nil {
		return nil, fmt.Errorf("nil agent spec")
	}
	client, err := f.ClientFor(spec.Model)
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
			Name:     spec.Name,
			Describe: spec.Description,
			Agent:    agent,
		})
	}
	return out, nil
}

// PrimaryClient returns the factory's primary client.
func (f *ClientFactory) PrimaryClient() providers.Client { return f.primaryClient }

// PrimaryConfig returns the factory's primary model config.
func (f *ClientFactory) PrimaryConfig() config.ModelConfig { return f.primaryCfg }
