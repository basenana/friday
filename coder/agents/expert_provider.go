package agents

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/subagents"
	"github.com/basenana/friday/core/tools"
)

// SpecValidator rejects a catalog generation before it becomes visible to a
// running session (for example when a spec names an unknown model).
type SpecValidator func(*AgentSpec) error

// ExpertProvider turns declarative specs into session-local runtime agents.
// It owns the compiled-agent cache; core/subagents only consumes its interface.
type ExpertProvider struct {
	mu              sync.Mutex
	specs           SpecProvider
	factory         *ClientFactory
	workspacePrompt string
	tools           []*tools.Tool
	validate        SpecValidator
	observed        string
	experts         []subagents.ExpertAgent
}

func NewExpertProvider(specs SpecProvider, factory *ClientFactory, workspacePrompt string, allTools []*tools.Tool, validate SpecValidator) (*ExpertProvider, error) {
	p := &ExpertProvider{
		specs: specs, factory: factory, workspacePrompt: workspacePrompt,
		tools: append([]*tools.Tool(nil), allTools...), validate: validate,
	}
	if err := p.refreshLocked(true); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *ExpertProvider) List() []subagents.ExpertAgent {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.refreshLocked(false); err != nil {
		logger.New("agents").Warnw("failed to rebuild hot-loaded agents", "error", err)
	}
	return append([]subagents.ExpertAgent(nil), p.experts...)
}

func (p *ExpertProvider) refreshLocked(force bool) error {
	if p.specs == nil || p.factory == nil {
		p.experts = nil
		return nil
	}
	specs := append([]*AgentSpec(nil), p.specs.List()...)
	sort.SliceStable(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	stamp, err := specSnapshotStamp(specs)
	if err != nil {
		return err
	}
	if !force && stamp == p.observed {
		return nil
	}
	p.observed = stamp
	composed := make([]*AgentSpec, 0, len(specs))
	for _, original := range specs {
		if original == nil {
			continue
		}
		if p.validate != nil {
			if err := p.validate(original); err != nil {
				return err
			}
		}
		spec := *original
		spec.SystemPrompt = ComposeSystemPrompt(p.workspacePrompt, original.SystemPrompt)
		composed = append(composed, &spec)
	}
	experts, err := p.factory.BuildExpertAgents(composed, p.tools)
	if err != nil {
		return err
	}
	p.experts = experts
	return nil
}

func specSnapshotStamp(specs []*AgentSpec) (string, error) {
	data, err := json.Marshal(specs)
	if err != nil {
		return "", fmt.Errorf("encode agent catalog: %w", err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

var _ subagents.AgentProvider = (*ExpertProvider)(nil)
