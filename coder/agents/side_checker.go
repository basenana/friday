package agents

import "git.hypo.xyz/agents/friday/core/providers"

// SideCheckerSpec returns the AgentSpec for the read-only side-checker
// subagent. Its deny-list policy keeps new read-only tools available while
// preventing checks from modifying files or running commands.
func SideCheckerSpec() *AgentSpec {
	return &AgentSpec{
		Name:         NameSideChecker,
		Description:  "Read-only side checker. Answers one focused question using the current conversation context and returns an evidence-based report. Cannot modify files.",
		Effort:       providers.ReasoningEffortNone,
		SystemPrompt: SideCheckerSystemPrompt,
		ToolPolicy: ToolPolicy{
			Deny: []string{
				ToolFsWrite,
				ToolFsEdit,
				ToolFsDelete,
				ToolBash,
				ToolBgTask,
				ToolKillTask,
			},
		},
		MaxLoopTimes: 50,
		Mode:         ModeSubagent,
	}
}
