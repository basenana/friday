package agents

import "github.com/basenana/friday/config"

// RegisterBuiltins registers the four built-in agent specs (explorer, planner,
// reviewer, advisor) into the given Registry. Each agent's model is resolved
// from cfg.AgentModel(name), falling back to the primary model when the user
// has not configured a per-agent override.
func RegisterBuiltins(reg *Registry, cfg *config.Config) {
	if reg == nil || cfg == nil {
		return
	}
	reg.Register(ExplorerSpec(cfg.AgentModel(NameExplorer)))
	reg.Register(PlannerSpec(cfg.AgentModel(NamePlanner)))
	reg.Register(ReviewerSpec(cfg.AgentModel(NameReviewer)))
	reg.Register(AdvisorSpec(cfg.AgentModel(NameAdvisor)))
}
