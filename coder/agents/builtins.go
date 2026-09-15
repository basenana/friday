package agents

import "github.com/basenana/friday/config"

// RegisterBuiltins registers the explorer spec. Its model is resolved from
// cfg.AgentModel(name), falling back to the primary model when the user has not
// configured a per-agent override.
func RegisterBuiltins(reg *Registry, cfg *config.Config) {
	if reg == nil || cfg == nil {
		return
	}
	reg.Register(ExplorerSpec(cfg.AgentModel(NameExplorer)))
}
