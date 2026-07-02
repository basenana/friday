package agents

import "github.com/basenana/friday/config"

// AdvisorSpec returns the AgentSpec for the advisor subagent.
//
// The advisor gives pragmatic, minimal advice. Read-only.
func AdvisorSpec(model config.ModelConfig) *AgentSpec {
	return &AgentSpec{
		Name:        NameAdvisor,
		Description: "Provides pragmatic, minimal advice with a bottom-line answer, action plan (<=7 steps), and effort estimate. Read-only.",
		Model:       model,
		SystemPrompt: AdvisorSystemPrompt,
		ToolPolicy: ToolPolicy{
			Allow: []string{
				ToolFsRead,
				ToolFsList,
			},
		},
		MaxLoopTimes: 20,
		Mode:         ModeSubagent,
	}
}
