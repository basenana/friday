package agents

import "sync"

// Registry stores AgentSpecs keyed by Name (case-sensitive).
type Registry struct {
	mu    sync.RWMutex
	specs map[string]*AgentSpec
}

func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]*AgentSpec)}
}

func (r *Registry) Register(spec *AgentSpec) {
	if spec == nil || spec.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs[spec.Name] = spec
}

func (r *Registry) Get(name string) (*AgentSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[name]
	return spec, ok
}

func (r *Registry) List() []*AgentSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*AgentSpec, 0, len(r.specs))
	for _, spec := range r.specs {
		out = append(out, spec)
	}
	return out
}
