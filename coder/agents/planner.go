package agents

import "github.com/basenana/friday/config"

// PlannerSpec returns the AgentSpec for the planner subagent.
//
// The planner interviews the user briefly then produces a structured plan.
// Read-only: only fs_read and fs_list are allowed.
func PlannerSpec(model config.ModelConfig) *AgentSpec {
	return &AgentSpec{
		Name:        NamePlanner,
		Description: "Interviews the user briefly and produces a structured implementation plan with steps, constraints, and anti-goals. Read-only.",
		Model:       model,
		SystemPrompt: PlannerSystemPrompt,
		ToolPolicy: ToolPolicy{
			Allow: []string{
				ToolFsRead,
				ToolFsList,
			},
		},
		MaxLoopTimes: 40,
		Mode:         ModeSubagent,
	}
}
