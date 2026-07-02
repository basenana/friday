package agents

import "github.com/basenana/friday/config"

// ExplorerSpec returns the AgentSpec for the read-only explorer subagent.
//
// The explorer is the strict-read-only investigator. Its ToolPolicy uses a
// deny list covering every write/execute tool, so any new read-only tool
// added later is automatically available.
func ExplorerSpec(model config.ModelConfig) *AgentSpec {
	return &AgentSpec{
		Name:        NameExplorer,
		Description: "Read-only investigator. Explores the codebase and returns a structured report with findings, files examined, and recommended next steps. Cannot modify files.",
		Model:       model,
		SystemPrompt: ExplorerSystemPrompt,
		ToolPolicy: ToolPolicy{
			Deny: []string{
				ToolFsWrite,
				ToolFsEdit,
				ToolFsMkdir,
				ToolFsDelete,
				ToolBash,
				ToolBgTask,
				ToolKillTask,
			},
		},
		MaxLoopTimes: 30,
		Mode:         ModeSubagent,
	}
}
